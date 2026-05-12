<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- Filters -->
      <div class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <Select v-model="currentFilter" :options="statusFilters" class="w-36" @change="fetchOrders" />
          <div class="flex flex-1 items-center justify-end gap-2">
            <button v-if="invoiceConfig.enabled" class="btn btn-secondary" :disabled="invoiceLoading" @click="openInvoiceDialog()">
              {{ t('payment.invoice.apply') }}
            </button>
            <button @click="fetchOrders" :disabled="loading" class="btn btn-secondary" :title="t('common.refresh')">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button class="btn btn-primary" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</button>
          </div>
        </div>
      </div>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="loading">
        <template #actions="{ row }">
          <div class="flex items-center gap-2">
            <button v-if="row.status === 'PENDING'" @click="handleCancel(row.id)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-yellow-600 hover:bg-yellow-50 dark:text-yellow-400 dark:hover:bg-yellow-900/20">
              <Icon name="x" size="sm" />
              <span>{{ t('payment.orders.cancel') }}</span>
            </button>
            <button v-if="canRequestRefund(row)" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
              <Icon name="dollar" size="sm" />
              <span>{{ t('payment.orders.requestRefund') }}</span>
            </button>
            <button v-if="invoiceConfig.enabled && row.status === 'COMPLETED'" @click="openInvoiceDialog(row.id)" class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
              <Icon name="document" size="sm" />
              <span>{{ t('payment.invoice.apply') }}</span>
            </button>
          </div>
        </template>
      </OrderTable>

      <!-- Pagination -->
      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <!-- Cancel Confirm Dialog -->
    <BaseDialog :show="!!cancelTargetId" :title="t('payment.orders.cancel')" width="narrow" @close="cancelTargetId = null">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('payment.confirmCancel') }}</p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="cancelTargetId = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-danger" :disabled="actionLoading" @click="confirmCancel">{{ actionLoading ? t('common.processing') : t('payment.orders.cancel') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- Refund Dialog -->
    <BaseDialog :show="!!refundTarget" :title="t('payment.orders.requestRefund')" @close="refundTarget = null">
      <div v-if="refundTarget" class="space-y-4">
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
          <div class="flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ refundTarget.id }}</span>
          </div>
          <div class="mt-2 flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</span>
            <span class="text-gray-900 dark:text-white">${{ refundTarget.amount.toFixed(2) }}</span>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('payment.refundReason') }}</label>
          <textarea v-model="refundReason" rows="3" class="input mt-1 w-full" :placeholder="t('payment.refundReasonPlaceholder')" />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="refundTarget = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="actionLoading || !refundReason.trim()" @click="confirmRefund">{{ actionLoading ? t('common.processing') : t('payment.orders.requestRefund') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- Invoice Dialog -->
    <BaseDialog :show="showInvoiceDialog" :title="t('payment.invoice.apply')" width="wide" @close="closeInvoiceDialog">
      <div class="space-y-5">
        <div class="rounded-xl border border-blue-100 bg-blue-50 p-4 text-sm text-blue-700 dark:border-blue-900/50 dark:bg-blue-950/30 dark:text-blue-200">
          {{ t('payment.invoice.autoIssueHint') }}
        </div>

        <div>
          <div class="mb-2 flex items-center justify-between">
            <label class="input-label">{{ t('payment.invoice.selectOrders') }}</label>
            <span class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.invoice.totalAmount') }}：¥{{ selectedInvoiceAmount.toFixed(2) }}</span>
          </div>
          <div class="max-h-56 space-y-2 overflow-y-auto rounded-xl border border-gray-200 p-3 dark:border-dark-700">
            <label v-for="order in invoiceEligibleOrders" :key="order.order_id" class="flex cursor-pointer items-center justify-between gap-3 rounded-lg p-2 hover:bg-gray-50 dark:hover:bg-dark-800">
              <div class="flex items-center gap-3">
                <input v-model="invoiceForm.order_ids" type="checkbox" :value="order.order_id" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
                <div>
                  <div class="font-mono text-sm text-gray-900 dark:text-white">#{{ order.order_id }} · {{ order.out_trade_no }}</div>
                  <div class="text-xs text-gray-500 dark:text-gray-400">{{ formatDate(order.created_at) }}</div>
                </div>
              </div>
              <span class="font-medium text-gray-900 dark:text-white">¥{{ order.pay_amount.toFixed(2) }}</span>
            </label>
            <div v-if="!invoiceLoading && invoiceEligibleOrders.length === 0" class="py-6 text-center text-sm text-gray-500 dark:text-gray-400">
              {{ t('payment.invoice.noEligibleOrders') }}
            </div>
            <div v-if="invoiceLoading" class="py-6 text-center text-sm text-gray-500 dark:text-gray-400">
              {{ t('common.loading') }}
            </div>
          </div>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('payment.invoice.buyerType') }}</label>
            <select v-model="invoiceForm.buyer_type" class="input mt-1 w-full">
              <option value="individual">{{ t('payment.invoice.individual') }}</option>
              <option value="enterprise">{{ t('payment.invoice.enterprise') }}</option>
            </select>
          </div>
          <div>
            <label class="input-label">{{ t('payment.invoice.buyerName') }}</label>
            <input v-model="invoiceForm.buyer_name" class="input mt-1 w-full" :placeholder="t('payment.invoice.buyerNamePlaceholder')" />
          </div>
          <div v-if="invoiceForm.buyer_type === 'enterprise'">
            <label class="input-label">{{ t('payment.invoice.buyerTaxNo') }}</label>
            <input v-model="invoiceForm.buyer_tax_no" class="input mt-1 w-full" />
          </div>
          <div>
            <label class="input-label">{{ t('payment.invoice.buyerEmail') }}</label>
            <input v-model="invoiceForm.buyer_email" class="input mt-1 w-full" type="email" />
          </div>
          <div>
            <label class="input-label">{{ t('payment.invoice.buyerPhone') }}</label>
            <input v-model="invoiceForm.buyer_phone" class="input mt-1 w-full" />
          </div>
          <div>
            <label class="input-label">{{ t('payment.invoice.buyerAddress') }}</label>
            <input v-model="invoiceForm.buyer_address" class="input mt-1 w-full" />
          </div>
          <div>
            <label class="input-label">{{ t('payment.invoice.buyerBankName') }}</label>
            <input v-model="invoiceForm.buyer_bank_name" class="input mt-1 w-full" />
          </div>
          <div>
            <label class="input-label">{{ t('payment.invoice.buyerBankAccount') }}</label>
            <input v-model="invoiceForm.buyer_bank_account" class="input mt-1 w-full" />
          </div>
        </div>

        <div>
          <label class="input-label">{{ t('payment.invoice.remark') }}</label>
          <textarea v-model="invoiceForm.remark" rows="2" class="input mt-1 w-full" />
        </div>

        <div v-if="invoiceApplications.length > 0" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
          <div class="mb-2 text-sm font-medium text-gray-900 dark:text-white">{{ t('payment.invoice.history') }}</div>
          <div class="space-y-2">
            <div v-for="invoice in invoiceApplications.slice(0, 5)" :key="invoice.id" class="flex items-center justify-between text-sm">
              <span>#{{ invoice.id }} · ¥{{ invoice.invoice_amount.toFixed(2) }} · {{ t('payment.invoice.status.' + invoice.status, invoice.status) }}</span>
              <a v-if="invoice.status === 'ISSUED'" class="text-primary-600 hover:underline" :href="paymentAPI.invoiceFileURL(invoice.id, 'pdf')" target="_blank">{{ t('payment.invoice.downloadPdf') }}</a>
            </div>
          </div>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="closeInvoiceDialog">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="actionLoading || !canSubmitInvoice" @click="submitInvoice">
            {{ actionLoading ? t('common.processing') : t('payment.invoice.submit') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { PaymentOrder, InvoiceApplication, InvoiceBuyerType, InvoiceEligibleOrder, InvoicePublicConfig } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import OrderTable from '@/components/payment/OrderTable.vue'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

const loading = ref(false)
const actionLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const refundEligibleProviders = ref<Set<string>>(new Set())
const currentFilter = ref('')
const cancelTargetId = ref<number | null>(null)
const refundTarget = ref<PaymentOrder | null>(null)
const refundReason = ref('')
const showInvoiceDialog = ref(false)
const invoiceLoading = ref(false)
const invoiceEligibleOrders = ref<InvoiceEligibleOrder[]>([])
const invoiceApplications = ref<InvoiceApplication[]>([])
const invoiceConfig = reactive<InvoicePublicConfig>({ enabled: false, auto_issue_enabled: false })
const invoiceForm = reactive({
  order_ids: [] as number[],
  buyer_type: 'individual' as InvoiceBuyerType,
  buyer_name: '',
  buyer_tax_no: '',
  buyer_email: '',
  buyer_phone: '',
  buyer_address: '',
  buyer_bank_name: '',
  buyer_bank_account: '',
  remark: '',
})
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

const statusFilters = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
])

async function fetchOrders() {
  loading.value = true
  try {
    const res = await paymentAPI.getMyOrders({
      page: pagination.page,
      page_size: pagination.page_size,
      status: currentFilter.value || undefined,
    })
    orders.value = res.data.items || []
    pagination.total = res.data.total || 0
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

function handlePageChange(page: number) { pagination.page = page; fetchOrders() }
function handlePageSizeChange(size: number) { pagination.page_size = size; pagination.page = 1; fetchOrders() }

function handleCancel(orderId: number) { cancelTargetId.value = orderId }

function formatDate(dateStr: string) { return new Date(dateStr).toLocaleString() }

async function confirmCancel() {
  if (!cancelTargetId.value) return
  actionLoading.value = true
  try {
    await paymentAPI.cancelOrder(cancelTargetId.value)
    appStore.showSuccess(t('common.success'))
    cancelTargetId.value = null
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function openRefundDialog(order: PaymentOrder) { refundTarget.value = order; refundReason.value = '' }

async function confirmRefund() {
  if (!refundTarget.value || !refundReason.value.trim()) return
  actionLoading.value = true
  try {
    await paymentAPI.requestRefund(refundTarget.value.id, { reason: refundReason.value.trim() })
    appStore.showSuccess(t('common.success'))
    refundTarget.value = null
    refundReason.value = ''
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function canRequestRefund(order: PaymentOrder): boolean {
  if (order.status !== 'COMPLETED') return false
  if (!order.provider_instance_id) return false
  return refundEligibleProviders.value.has(order.provider_instance_id)
}

async function loadRefundEligibility() {
  try {
    const res = await paymentAPI.getRefundEligibleProviders()
    refundEligibleProviders.value = new Set(res.data.provider_instance_ids || [])
  } catch { /* ignore — default to hiding refund button */ }
}

async function loadInvoiceConfig() {
  try {
    const res = await paymentAPI.getInvoiceConfig()
    invoiceConfig.enabled = !!res.data.enabled
    invoiceConfig.auto_issue_enabled = !!res.data.auto_issue_enabled
  } catch {
    invoiceConfig.enabled = false
    invoiceConfig.auto_issue_enabled = false
  }
}

const selectedInvoiceAmount = computed(() => {
  const selected = new Set(invoiceForm.order_ids)
  return invoiceEligibleOrders.value.reduce((sum, order) => selected.has(order.order_id) ? sum + order.pay_amount : sum, 0)
})

const canSubmitInvoice = computed(() => {
  if (invoiceForm.order_ids.length === 0) return false
  if (!invoiceForm.buyer_name.trim()) return false
  if (invoiceForm.buyer_type === 'enterprise' && !invoiceForm.buyer_tax_no.trim()) return false
  if (!invoiceForm.buyer_email.trim() && !invoiceForm.buyer_phone.trim()) return false
  return true
})

async function openInvoiceDialog(preselectOrderId?: number) {
  if (!invoiceConfig.enabled) return
  showInvoiceDialog.value = true
  invoiceLoading.value = true
  try {
    const [eligibleRes, invoicesRes] = await Promise.all([
      paymentAPI.getInvoiceEligibleOrders(),
      paymentAPI.getMyInvoices({ page: 1, page_size: 20 }),
    ])
    invoiceEligibleOrders.value = eligibleRes.data || []
    invoiceApplications.value = invoicesRes.data.items || []
    if (preselectOrderId && invoiceEligibleOrders.value.some(order => order.order_id === preselectOrderId)) {
      invoiceForm.order_ids = [preselectOrderId]
    }
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    invoiceLoading.value = false
  }
}

function closeInvoiceDialog() {
  showInvoiceDialog.value = false
}

async function submitInvoice() {
  if (!canSubmitInvoice.value) return
  actionLoading.value = true
  try {
    const res = await paymentAPI.createInvoiceApplication({
      order_ids: invoiceForm.order_ids,
      buyer_type: invoiceForm.buyer_type,
      buyer_name: invoiceForm.buyer_name.trim(),
      buyer_tax_no: invoiceForm.buyer_tax_no.trim(),
      buyer_email: invoiceForm.buyer_email.trim(),
      buyer_phone: invoiceForm.buyer_phone.trim(),
      buyer_address: invoiceForm.buyer_address.trim(),
      buyer_bank_name: invoiceForm.buyer_bank_name.trim(),
      buyer_bank_account: invoiceForm.buyer_bank_account.trim(),
      remark: invoiceForm.remark.trim(),
    })
    appStore.showSuccess(t('payment.invoice.submitted'))
    invoiceApplications.value = [res.data, ...invoiceApplications.value]
    invoiceEligibleOrders.value = invoiceEligibleOrders.value.filter(order => !invoiceForm.order_ids.includes(order.order_id))
    invoiceForm.order_ids = []
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

onMounted(() => { fetchOrders(); loadRefundEligibility(); loadInvoiceConfig() })
</script>
