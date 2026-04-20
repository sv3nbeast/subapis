<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl">
      <div class="relative overflow-hidden rounded-[28px] border border-gray-200/80 bg-white/95 p-5 shadow-2xl shadow-slate-200/40 backdrop-blur dark:border-dark-700 dark:bg-dark-900/90 dark:shadow-black/25 sm:p-6 lg:p-8">
        <div class="pointer-events-none absolute -left-16 -top-16 h-48 w-48 rounded-full bg-emerald-200/30 blur-3xl dark:bg-emerald-500/10"></div>
        <div class="pointer-events-none absolute -right-20 top-1/3 h-56 w-56 rounded-full bg-blue-200/30 blur-3xl dark:bg-blue-500/10"></div>

        <div class="relative space-y-6">
          <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
            <div class="max-w-3xl">
              <span class="inline-flex items-center rounded-full bg-emerald-50 px-3 py-1 text-[11px] font-semibold tracking-[0.18em] text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-200">
                {{ t('payment.secureCheckout') }}
              </span>
              <h1 class="mt-3 text-2xl font-semibold tracking-tight text-gray-900 dark:text-white sm:text-3xl">
                {{ t('payment.title') }}
              </h1>
              <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400 sm:text-base">
                {{ t('payment.subtitle') }}
              </p>
            </div>
            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-xl border border-gray-200 bg-white px-4 py-2.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700"
                :disabled="loading"
                @click="refreshCheckout"
              >
                <svg class="h-4 w-4" :class="loading ? 'animate-spin' : ''" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m14.836 2A8.001 8.001 0 005.582 9m0 0H9m11 11v-5h-.581m0 0A8.003 8.003 0 0118.418 15m0 0H15" />
                </svg>
                {{ t('common.refresh') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-xl border border-gray-200 bg-white px-4 py-2.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700"
                @click="router.push('/orders')"
              >
                <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                </svg>
                {{ t('nav.myOrders') }}
              </button>
            </div>
          </div>

          <div class="grid gap-3 md:grid-cols-3">
            <div class="rounded-2xl border border-gray-200/80 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-800/70">
              <p class="text-xs font-medium uppercase tracking-[0.2em] text-gray-400 dark:text-gray-500">{{ t('payment.currentBalance') }}</p>
              <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{{ user?.balance?.toFixed(2) || '0.00' }}</p>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ user?.username || t('payment.rechargeAccount') }}</p>
            </div>
            <div class="rounded-2xl border border-gray-200/80 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-800/70">
              <p class="text-xs font-medium uppercase tracking-[0.2em] text-gray-400 dark:text-gray-500">{{ t('payment.paymentMethod') }}</p>
              <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{{ enabledMethods.length }}</p>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ enabledMethods.length > 0 ? t('payment.tabTopUp') : t('payment.notAvailable') }}</p>
            </div>
            <div class="rounded-2xl border border-gray-200/80 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-800/70">
              <p class="text-xs font-medium uppercase tracking-[0.2em] text-gray-400 dark:text-gray-500">{{ activeSubscriptions.length > 0 ? t('payment.activeSubscription') : t('payment.tabSubscribe') }}</p>
              <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{{ activeSubscriptions.length > 0 ? activeSubscriptions.length : checkout.plans.length }}</p>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ activeSubscriptions.length > 0 ? t('payment.renewNow') : t('payment.selectPlan') }}</p>
            </div>
          </div>

          <div v-if="loading" class="flex items-center justify-center py-20">
            <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
          </div>

          <template v-else>
            <!-- Tab Switcher (hide during payment and subscription confirm) -->
            <div v-if="tabs.length > 1 && paymentPhase === 'select' && !selectedPlan" class="inline-flex rounded-xl border border-gray-200 bg-gray-100/90 p-1 dark:border-dark-700 dark:bg-dark-800/90">
              <button
                v-for="tab in tabs"
                :key="tab.key"
                class="min-w-[8rem] rounded-lg px-5 py-2.5 text-sm font-medium transition-all"
                :class="activeTab === tab.key ? 'bg-white text-gray-900 shadow dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'"
                @click="activeTab = tab.key"
              >
                {{ tab.label }}
              </button>
            </div>

            <!-- Payment in progress (shared by recharge and subscription) -->
            <template v-if="paymentPhase === 'paying'">
              <PaymentStatusPanel
                :order-id="paymentState.orderId"
                :qr-code="paymentState.qrCode"
                :expires-at="paymentState.expiresAt"
                :payment-type="paymentState.paymentType"
                :pay-url="paymentState.payUrl"
                :order-type="paymentState.orderType"
                @done="onPaymentDone"
                @success="onPaymentSuccess"
              />
            </template>
            <template v-else-if="paymentPhase === 'stripe'">
              <StripePaymentInline
                :order-id="paymentState.orderId"
                :amount="paymentState.amount"
                :client-secret="paymentState.clientSecret"
                :order-type="paymentState.orderType || undefined"
                :publishable-key="checkout.stripe_publishable_key"
                :pay-amount="paymentState.payAmount"
                @success="onPaymentSuccess"
                @done="onStripeDone"
                @back="resetPayment"
                @redirect="onStripeRedirect"
              />
            </template>

            <!-- Tab content (select phase) -->
            <template v-else>
              <!-- Top-up Tab -->
              <template v-if="activeTab === 'recharge'">
                <div class="grid gap-6 xl:grid-cols-[minmax(0,1.15fr)_minmax(320px,0.85fr)]">
                  <div class="space-y-6">
                    <div class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                      <p class="text-xs font-medium uppercase tracking-[0.2em] text-gray-400 dark:text-gray-500">{{ t('payment.rechargeAccount') }}</p>
                      <p class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">{{ user?.username || '' }}</p>
                      <p class="mt-1 text-sm font-medium text-green-600 dark:text-green-400">{{ t('payment.currentBalance') }}: {{ user?.balance?.toFixed(2) || '0.00' }}</p>
                    </div>

                    <div v-if="enabledMethods.length === 0" class="rounded-2xl border border-gray-200 bg-white py-16 text-center shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                      <p class="text-gray-500 dark:text-gray-400">{{ t('payment.notAvailable') }}</p>
                    </div>

                    <template v-else>
                      <div class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                        <AmountInput
                          v-model="amount"
                          :amounts="[10, 20, 50, 100, 200, 500, 1000, 2000, 5000]"
                          :min="globalMinAmount"
                          :max="globalMaxAmount"
                        />
                        <p v-if="amountError" class="mt-3 text-xs text-amber-600 dark:text-amber-300">{{ amountError }}</p>
                      </div>

                      <div v-if="enabledMethods.length >= 1" class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                        <PaymentMethodSelector
                          :methods="methodOptions"
                          :selected="selectedMethod"
                          @select="selectedMethod = $event"
                        />
                      </div>
                    </template>
                  </div>

                  <div class="space-y-6">
                    <div class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                      <div class="space-y-3 text-sm">
                        <div class="flex justify-between">
                          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.paymentAmount') }}</span>
                          <span class="text-gray-900 dark:text-white">¥{{ validAmount.toFixed(2) }}</span>
                        </div>
                        <div v-if="feeRate > 0" class="flex justify-between">
                          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.fee') }} ({{ feeRate }}%)</span>
                          <span class="text-gray-900 dark:text-white">¥{{ feeAmount.toFixed(2) }}</span>
                        </div>
                        <div class="flex justify-between border-t border-gray-200 pt-3 dark:border-dark-600">
                          <span class="font-medium text-gray-700 dark:text-gray-300">{{ t('payment.actualPay') }}</span>
                          <span class="text-lg font-bold text-primary-600 dark:text-primary-400">¥{{ totalAmount.toFixed(2) }}</span>
                        </div>
                        <div v-if="balanceRechargeMultiplier !== 1" class="flex justify-between border-t border-gray-200 pt-3 dark:border-dark-600">
                          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.creditedBalance') }}</span>
                          <span class="text-gray-900 dark:text-white">${{ creditedAmount.toFixed(2) }}</span>
                        </div>
                        <p v-if="balanceRechargeMultiplier !== 1" class="text-xs text-gray-500 dark:text-gray-400">
                          {{ t('payment.rechargeRatePreview', { usd: balanceRechargeMultiplier.toFixed(2) }) }}
                        </p>
                      </div>
                    </div>

                    <button :class="['btn w-full py-3 text-base font-medium', paymentButtonClass]" :disabled="!canSubmit || submitting" @click="handleSubmitRecharge">
                      <span v-if="submitting" class="flex items-center justify-center gap-2">
                        <span class="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent"></span>
                        {{ t('common.processing') }}
                      </span>
                      <span v-else>{{ t('payment.createOrder') }} ¥{{ totalAmount.toFixed(2) }}</span>
                    </button>

                    <div v-if="errorMessage" class="rounded-xl border border-red-200 bg-red-50 p-4 dark:border-red-800/50 dark:bg-red-900/20">
                      <p class="text-sm text-red-700 dark:text-red-400">{{ errorMessage }}</p>
                    </div>
                  </div>
                </div>
              </template>

              <!-- Subscribe Tab -->
              <template v-else-if="activeTab === 'subscription'">
                <!-- Subscription confirm (inline, replaces plan list) -->
                <template v-if="selectedPlan">
                  <div class="grid gap-6 xl:grid-cols-[minmax(0,1.15fr)_minmax(320px,0.85fr)]">
                    <div class="space-y-6">
                      <div class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                        <div class="mb-4 flex flex-wrap items-center gap-2">
                          <span :class="['rounded-full border px-2.5 py-1 text-xs font-semibold', planBadgeClass]">
                            {{ platformLabel(selectedPlan.group_platform || '') }}
                          </span>
                          <span class="text-xs font-medium uppercase tracking-[0.2em] text-gray-400 dark:text-gray-500">{{ t('payment.confirmSubscription') }}</span>
                        </div>
                        <h3 class="text-2xl font-bold text-gray-900 dark:text-white">{{ selectedPlan.name }}</h3>
                        <div class="mt-4 flex items-end gap-2">
                          <span v-if="selectedPlan.original_price" class="text-sm text-gray-400 line-through dark:text-gray-500">
                            ¥{{ selectedPlan.original_price }}
                          </span>
                          <span :class="['text-4xl font-black tracking-tight', planTextClass]">¥{{ selectedPlan.price }}</span>
                          <span class="pb-1 text-sm text-gray-500 dark:text-gray-400">/ {{ planValiditySuffix }}</span>
                        </div>
                        <p v-if="selectedPlan.description" class="mt-3 text-sm leading-7 text-gray-500 dark:text-gray-400">
                          {{ selectedPlan.description }}
                        </p>
                        <div class="mt-5 grid grid-cols-2 gap-3 rounded-2xl bg-gray-50/90 p-4 dark:bg-dark-700/50">
                          <div>
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.rate') }}</span>
                            <div class="mt-1 flex items-baseline">
                              <span :class="['text-lg font-bold', planTextClass]">×{{ selectedPlan.rate_multiplier ?? 1 }}</span>
                            </div>
                          </div>
                          <div v-if="selectedPlan.daily_limit_usd != null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.dailyLimit') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">${{ selectedPlan.daily_limit_usd }}</div>
                          </div>
                          <div v-if="selectedPlan.weekly_limit_usd != null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.weeklyLimit') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">${{ selectedPlan.weekly_limit_usd }}</div>
                          </div>
                          <div v-if="selectedPlan.monthly_limit_usd != null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.monthlyLimit') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">${{ selectedPlan.monthly_limit_usd }}</div>
                          </div>
                          <div v-if="selectedPlan.daily_limit_usd == null && selectedPlan.weekly_limit_usd == null && selectedPlan.monthly_limit_usd == null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.quota') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">{{ t('payment.planCard.unlimited') }}</div>
                          </div>
                        </div>
                      </div>
                    </div>

                    <div class="space-y-4">
                      <div v-if="enabledMethods.length >= 1" class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                        <PaymentMethodSelector
                          :methods="subMethodOptions"
                          :selected="selectedMethod"
                          @select="selectedMethod = $event"
                        />
                      </div>

                      <div class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                        <div class="space-y-3 text-sm">
                          <div class="flex justify-between">
                            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.amountLabel') }}</span>
                            <span class="text-gray-900 dark:text-white">¥{{ selectedPlan.price.toFixed(2) }}</span>
                          </div>
                          <div v-if="feeRate > 0" class="flex justify-between">
                            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.fee') }} ({{ feeRate }}%)</span>
                            <span class="text-gray-900 dark:text-white">¥{{ subFeeAmount.toFixed(2) }}</span>
                          </div>
                          <div class="flex justify-between border-t border-gray-200 pt-3 dark:border-dark-600">
                            <span class="font-medium text-gray-700 dark:text-gray-300">{{ t('payment.actualPay') }}</span>
                            <span class="text-lg font-bold text-primary-600 dark:text-primary-400">¥{{ subTotalAmount.toFixed(2) }}</span>
                          </div>
                        </div>
                      </div>

                      <div class="space-y-3 rounded-2xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                        <button :class="['btn w-full py-3 text-base font-medium', paymentButtonClass]" :disabled="!canSubmitSubscription || submitting" @click="confirmSubscribe">
                          <span v-if="submitting" class="flex items-center justify-center gap-2">
                            <span class="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent"></span>
                            {{ t('common.processing') }}
                          </span>
                          <span v-else>{{ t('payment.createOrder') }} ¥{{ (feeRate > 0 ? subTotalAmount : selectedPlan.price).toFixed(2) }}</span>
                        </button>
                        <button class="btn btn-secondary w-full" @click="selectedPlan = null">{{ t('common.cancel') }}</button>
                      </div>

                      <div v-if="errorMessage" class="rounded-xl border border-red-200 bg-red-50 p-4 dark:border-red-800/50 dark:bg-red-900/20">
                        <p class="text-sm text-red-700 dark:text-red-400">{{ errorMessage }}</p>
                      </div>
                    </div>
                  </div>
                </template>

                <!-- Plan list -->
                <template v-else>
                  <div v-if="checkout.plans.length === 0" class="rounded-2xl border border-gray-200 bg-white py-16 text-center shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
                    <Icon name="gift" size="xl" class="mx-auto mb-3 text-gray-300 dark:text-dark-600" />
                    <p class="text-gray-500 dark:text-gray-400">{{ t('payment.noPlans') }}</p>
                  </div>
                  <template v-else>
                    <div class="flex items-end justify-between gap-4">
                      <div>
                        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.selectPlan') }}</h2>
                        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('payment.subtitle') }}</p>
                      </div>
                      <span class="text-sm font-medium text-gray-400 dark:text-gray-500">{{ checkout.plans.length }} {{ t('payment.tabSubscribe') }}</span>
                    </div>
                    <div :class="planGridClass">
                      <SubscriptionPlanCard
                        v-for="plan in checkout.plans"
                        :key="plan.id"
                        :plan="plan"
                        :active-subscriptions="activeSubscriptions"
                        @select="selectPlan"
                      />
                    </div>
                  </template>

                  <!-- Active subscriptions -->
                  <div v-if="activeSubscriptions.length > 0" class="space-y-4">
                    <div class="flex items-end justify-between gap-4">
                      <div>
                        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.activeSubscription') }}</h2>
                        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('payment.renewNow') }}</p>
                      </div>
                      <span class="text-sm font-medium text-gray-400 dark:text-gray-500">{{ activeSubscriptions.length }}</span>
                    </div>
                    <div class="grid gap-4 lg:grid-cols-2">
                      <div
                        v-for="sub in activeSubscriptions"
                        :key="sub.id"
                        class="rounded-2xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800/90"
                      >
                        <div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                          <div class="min-w-0">
                            <div class="mb-2 flex flex-wrap items-center gap-2">
                              <span :class="['rounded-full px-2 py-0.5 text-[11px] font-medium', platformBadgeLightClass(sub.group?.platform || '')]">
                                {{ platformLabel(sub.group?.platform || '') }}
                              </span>
                              <span class="truncate text-base font-semibold text-gray-900 dark:text-white">
                                {{ sub.group?.name || `Group #${sub.group_id}` }}
                              </span>
                              <span class="badge badge-success shrink-0 text-[10px]">{{ t('userSubscriptions.status.active') }}</span>
                            </div>
                            <p class="text-sm text-gray-500 dark:text-gray-400">
                              <template v-if="sub.expires_at">
                                {{ t('userSubscriptions.daysRemaining', { days: getDaysRemaining(sub.expires_at) }) }}
                              </template>
                              <template v-else>
                                {{ t('userSubscriptions.noExpiration') }}
                              </template>
                            </p>
                          </div>
                          <button type="button" class="btn btn-secondary btn-sm shrink-0" @click="openRenewalForGroup(sub.group_id)">
                            {{ t('payment.renewNow') }}
                          </button>
                        </div>
                        <div class="grid grid-cols-2 gap-3 rounded-2xl bg-gray-50/90 p-4 dark:bg-dark-700/50">
                          <div>
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.rate') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">×{{ sub.group?.rate_multiplier ?? 1 }}</div>
                          </div>
                          <div v-if="sub.group?.daily_limit_usd != null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.dailyLimit') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">${{ sub.group.daily_limit_usd }}</div>
                          </div>
                          <div v-if="sub.group?.weekly_limit_usd != null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.weeklyLimit') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">${{ sub.group.weekly_limit_usd }}</div>
                          </div>
                          <div v-if="sub.group?.monthly_limit_usd != null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.monthlyLimit') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">${{ sub.group.monthly_limit_usd }}</div>
                          </div>
                          <div v-if="sub.group?.daily_limit_usd == null && sub.group?.weekly_limit_usd == null && sub.group?.monthly_limit_usd == null">
                            <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.quota') }}</span>
                            <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">{{ t('payment.planCard.unlimited') }}</div>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </template>
              </template>
            </template>

            <div v-if="(checkout.help_text || checkout.help_image_url) && paymentPhase === 'select' && !selectedPlan" class="rounded-2xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800/90">
              <div class="flex flex-col items-center gap-3">
                <img
                  v-if="checkout.help_image_url"
                  :src="checkout.help_image_url"
                  alt=""
                  class="h-40 max-w-full cursor-pointer rounded-lg object-contain transition-opacity hover:opacity-80"
                  @click="previewImage = checkout.help_image_url"
                />
                <p v-if="checkout.help_text" class="text-center text-sm text-gray-500 dark:text-gray-400">{{ checkout.help_text }}</p>
              </div>
            </div>
          </template>
        </div>
      </div>
    </div>
    <!-- Renewal Plan Selection Modal -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="showRenewalModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4" @click.self="closeRenewalModal">
          <div class="relative w-full max-w-5xl rounded-2xl border border-gray-200 bg-white p-6 shadow-2xl dark:border-dark-700 dark:bg-dark-900">
            <!-- Close button -->
            <button class="absolute right-4 top-4 rounded-lg p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-200" @click="closeRenewalModal">
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
            </button>
            <h3 class="mb-4 text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.selectPlan') }}</h3>
            <div class="grid gap-6 md:grid-cols-2 xl:grid-cols-3">
              <SubscriptionPlanCard v-for="plan in renewalPlans" :key="plan.id" :plan="plan" :active-subscriptions="activeSubscriptions" @select="selectPlanFromModal" />
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
    <!-- Image Preview Overlay -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="previewImage" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 backdrop-blur-sm" @click="previewImage = ''">
          <img :src="previewImage" alt="" class="max-h-[85vh] max-w-[90vw] rounded-xl object-contain shadow-2xl" />
        </div>
      </Transition>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePaymentStore } from '@/stores/payment'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractApiErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import type { SubscriptionPlan, CheckoutInfoResponse, OrderType } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import AmountInput from '@/components/payment/AmountInput.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import { METHOD_ORDER } from '@/components/payment/providerConfig'
import { platformBadgeLightClass, platformBadgeClass, platformTextClass, platformLabel } from '@/utils/platformColors'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import StripePaymentInline from '@/components/payment/StripePaymentInline.vue'
import Icon from '@/components/icons/Icon.vue'
import type { PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const paymentStore = usePaymentStore()
const subscriptionStore = useSubscriptionStore()
const appStore = useAppStore()

const user = computed(() => authStore.user)
const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)

function getDaysRemaining(expiresAt: string): number {
  const diff = new Date(expiresAt).getTime() - Date.now()
  return Math.max(0, Math.ceil(diff / (1000 * 60 * 60 * 24)))
}

const loading = ref(true)
const submitting = ref(false)
const errorMessage = ref('')
const activeTab = ref<'recharge' | 'subscription'>('recharge')
const amount = ref<number | null>(null)
const selectedMethod = ref('')
const selectedPlan = ref<SubscriptionPlan | null>(null)
const previewImage = ref('')

// Payment phase: 'select' → 'paying' (QR/redirect) or 'stripe' (inline Stripe)
const paymentPhase = ref<'select' | 'paying' | 'stripe'>('select')
const paymentState = ref<{
  orderId: number
  amount: number
  qrCode: string
  expiresAt: string
  paymentType: string
  payUrl: string
  clientSecret: string
  payAmount: number
  orderType: OrderType | ''
}>({ orderId: 0, amount: 0, qrCode: '', expiresAt: '', paymentType: '', payUrl: '', clientSecret: '', payAmount: 0, orderType: '' })

function resetPayment() {
  paymentPhase.value = 'select'
  paymentState.value = { orderId: 0, amount: 0, qrCode: '', expiresAt: '', paymentType: '', payUrl: '', clientSecret: '', payAmount: 0, orderType: '' }
}

function onPaymentDone() {
  const wasSubscription = paymentState.value.orderType === 'subscription'
  resetPayment()
  selectedPlan.value = null
  if (wasSubscription) {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function onPaymentSuccess() {
  authStore.refreshUser()
  if (paymentState.value.orderType === 'subscription') {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function onStripeDone() {
  const wasSubscription = paymentState.value.orderType === 'subscription'
  resetPayment()
  selectedPlan.value = null
  if (wasSubscription) {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

function onStripeRedirect(orderId: number, payUrl: string) {
  paymentState.value = { ...paymentState.value, orderId, payUrl, qrCode: '' }
  paymentPhase.value = 'paying'
}

// All checkout data from single API call
const checkout = ref<CheckoutInfoResponse>({
  methods: {}, global_min: 0, global_max: 0,
  plans: [], balance_disabled: false, balance_recharge_multiplier: 1, recharge_fee_rate: 0, help_text: '', help_image_url: '', stripe_publishable_key: '',
})

const tabs = computed(() => {
  const result: { key: 'recharge' | 'subscription'; label: string }[] = []
  if (!checkout.value.balance_disabled) result.push({ key: 'recharge', label: t('payment.tabTopUp') })
  result.push({ key: 'subscription', label: t('payment.tabSubscribe') })
  return result
})

const enabledMethods = computed(() => Object.keys(checkout.value.methods))
const validAmount = computed(() => amount.value ?? 0)
const balanceRechargeMultiplier = computed(() => {
  const multiplier = checkout.value.balance_recharge_multiplier
  return multiplier > 0 ? multiplier : 1
})
const creditedAmount = computed(() => Math.round((validAmount.value * balanceRechargeMultiplier.value) * 100) / 100)

// Adaptive grid: center single card, 2-col for 2 plans, 3-col for 3+
const planGridClass = computed(() => {
  const n = checkout.value.plans.length
  if (n <= 1) return 'grid grid-cols-1 gap-6'
  if (n === 2) return 'grid grid-cols-1 gap-6 md:grid-cols-2'
  return 'grid grid-cols-1 gap-6 md:grid-cols-2 xl:grid-cols-3'
})

// Check if an amount fits a method's [min, max]. 0 = no limit.
function amountFitsMethod(amt: number, methodType: string): boolean {
  if (amt <= 0) return true
  const ml = checkout.value.methods[methodType]
  if (!ml) return false
  if (ml.single_min > 0 && amt < ml.single_min) return false
  if (ml.single_max > 0 && amt > ml.single_max) return false
  return true
}

// Global range for AmountInput (union of all methods, precomputed by backend)
const globalMinAmount = computed(() => checkout.value.global_min)
const globalMaxAmount = computed(() => checkout.value.global_max)

// Selected method's limits (for validation and error messages)
const selectedLimit = computed(() => checkout.value.methods[selectedMethod.value])

const methodOptions = computed<PaymentMethodOption[]>(() =>
  enabledMethods.value.map((type) => {
    const ml = checkout.value.methods[type]
    return {
      type,
      fee_rate: ml?.fee_rate ?? 0,
      available: ml?.available !== false && amountFitsMethod(validAmount.value, type),
    }
  })
)

const feeRate = computed(() => checkout.value?.recharge_fee_rate ?? 0)
const feeAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.ceil(((validAmount.value * feeRate.value) / 100) * 100) / 100
    : 0
)
const totalAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.round((validAmount.value + feeAmount.value) * 100) / 100
    : validAmount.value
)

const amountError = computed(() => {
  if (validAmount.value <= 0) return ''
  // No method can handle this amount
  if (!enabledMethods.value.some((m) => amountFitsMethod(validAmount.value, m))) {
    return t('payment.amountNoMethod')
  }
  // Selected method can't handle this amount (but others can)
  const ml = selectedLimit.value
  if (ml) {
    if (ml.single_min > 0 && validAmount.value < ml.single_min) return t('payment.amountTooLow', { min: ml.single_min })
    if (ml.single_max > 0 && validAmount.value > ml.single_max) return t('payment.amountTooHigh', { max: ml.single_max })
  }
  return ''
})

const canSubmit = computed(() =>
  validAmount.value > 0
    && amountFitsMethod(validAmount.value, selectedMethod.value)
    && selectedLimit.value?.available !== false
)

// Subscription-specific: method options based on plan price
const subMethodOptions = computed<PaymentMethodOption[]>(() => {
  const planPrice = selectedPlan.value?.price ?? 0
  return enabledMethods.value.map((type) => {
    const ml = checkout.value.methods[type]
    return {
      type,
      fee_rate: ml?.fee_rate ?? 0,
      available: ml?.available !== false && amountFitsMethod(planPrice, type),
    }
  })
})

const subFeeAmount = computed(() => {
  const price = selectedPlan.value?.price ?? 0
  if (feeRate.value <= 0 || price <= 0) return 0
  return Math.ceil(((price * feeRate.value) / 100) * 100) / 100
})

const subTotalAmount = computed(() => {
  const price = selectedPlan.value?.price ?? 0
  if (feeRate.value <= 0 || price <= 0) return price
  return Math.round((price + subFeeAmount.value) * 100) / 100
})

const canSubmitSubscription = computed(() =>
  selectedPlan.value !== null
    && amountFitsMethod(selectedPlan.value.price, selectedMethod.value)
    && selectedLimit.value?.available !== false
)

// Auto-switch to first available method when current selection can't handle the amount
watch(() => [validAmount.value, selectedMethod.value] as const, ([amt, method]) => {
  if (amt <= 0 || amountFitsMethod(amt, method)) return
  const available = enabledMethods.value.find((m) => amountFitsMethod(amt, m))
  if (available) selectedMethod.value = available
})

// Payment button class: follows selected payment method color
const paymentButtonClass = computed(() => {
  const m = selectedMethod.value
  if (!m) return 'btn-primary'
  if (m.includes('alipay')) return 'btn-alipay'
  if (m.includes('wxpay')) return 'btn-wxpay'
  if (m === 'stripe') return 'btn-stripe'
  return 'btn-primary'
})

// Subscription confirm: platform accent colors (clean card, no gradient)
const planBadgeClass = computed(() => platformBadgeClass(selectedPlan.value?.group_platform || ''))
const planTextClass = computed(() => platformTextClass(selectedPlan.value?.group_platform || ''))

// Renewal modal state
const showRenewalModal = ref(false)
const renewGroupId = ref<number | null>(null)
const renewalPlans = computed(() => {
  if (renewGroupId.value == null) return []
  return checkout.value.plans.filter(p => p.group_id === renewGroupId.value)
})

const planValiditySuffix = computed(() => {
  if (!selectedPlan.value) return ''
  const u = selectedPlan.value.validity_unit || 'day'
  if (u === 'month') return t('payment.perMonth')
  if (u === 'year') return t('payment.perYear')
  return `${selectedPlan.value.validity_days}${t('payment.days')}`
})

function selectPlan(plan: SubscriptionPlan) {
  selectedPlan.value = plan
  errorMessage.value = ''
}

function selectPlanFromModal(plan: SubscriptionPlan) {
  showRenewalModal.value = false
  renewGroupId.value = null
  selectedPlan.value = plan
  errorMessage.value = ''
}

function closeRenewalModal() {
  showRenewalModal.value = false
  renewGroupId.value = null
}

function openRenewalForGroup(groupId: number) {
  const groupPlans = checkout.value.plans.filter(p => p.group_id === groupId)
  if (groupPlans.length === 1) {
    selectedPlan.value = groupPlans[0]
    return
  }
  if (groupPlans.length > 1) {
    renewGroupId.value = groupId
    showRenewalModal.value = true
  }
}

async function handleSubmitRecharge() {
  if (!canSubmit.value || submitting.value) return
  await createOrder(validAmount.value, 'balance')
}

async function confirmSubscribe() {
  if (!selectedPlan.value || submitting.value) return
  await createOrder(selectedPlan.value.price, 'subscription', selectedPlan.value.id)
}

async function createOrder(orderAmount: number, orderType: OrderType, planId?: number) {
  submitting.value = true
  errorMessage.value = ''
  try {
    const result = await paymentStore.createOrder({
      amount: orderAmount,
      payment_type: selectedMethod.value,
      order_type: orderType,
      plan_id: planId,
    })
    if (result.client_secret) {
      // Stripe: show Payment Element inline (user picks method → confirms → redirect if needed)
      paymentState.value = {
        orderId: result.order_id, amount: result.amount, qrCode: '', expiresAt: result.expires_at || '',
        paymentType: selectedMethod.value, payUrl: '',
        clientSecret: result.client_secret, payAmount: result.pay_amount,
        orderType,
      }
      paymentPhase.value = 'stripe'
    } else if (isMobileDevice() && result.pay_url) {
      // Mobile + pay_url: redirect directly instead of QR/popup (mobile browsers block popups)
      paymentState.value = {
        orderId: result.order_id, amount: result.amount, qrCode: '', expiresAt: result.expires_at || '',
        paymentType: selectedMethod.value, payUrl: result.pay_url,
        clientSecret: '', payAmount: 0,
        orderType,
      }
      paymentPhase.value = 'paying'
      window.location.href = result.pay_url
      return
    } else if (result.qr_code) {
      // QR mode: show QR code inline
      paymentState.value = {
        orderId: result.order_id, amount: result.amount, qrCode: result.qr_code,
        expiresAt: result.expires_at || '', paymentType: selectedMethod.value, payUrl: '',
        clientSecret: '', payAmount: 0,
        orderType,
      }
      paymentPhase.value = 'paying'
    } else if (result.pay_url) {
      // Desktop fallback: when upstream only returns a payment URL, render it as an inline QR
      // instead of forcing a popup/new tab.
      paymentState.value = {
        orderId: result.order_id, amount: result.amount, qrCode: '', expiresAt: result.expires_at || '',
        paymentType: selectedMethod.value, payUrl: result.pay_url,
        clientSecret: '', payAmount: 0,
        orderType,
      }
      paymentPhase.value = 'paying'
    } else {
      errorMessage.value = t('payment.result.failed')
      appStore.showError(errorMessage.value)
    }
  } catch (err: unknown) {
    const apiErr = err as Record<string, unknown>
    if (apiErr.reason === 'TOO_MANY_PENDING') {
      const metadata = apiErr.metadata as Record<string, unknown> | undefined
      errorMessage.value = t('payment.errors.tooManyPending', { max: metadata?.max || '' })
    } else if (apiErr.reason === 'CANCEL_RATE_LIMITED') {
      errorMessage.value = t('payment.errors.cancelRateLimited')
    } else {
      errorMessage.value = extractApiErrorMessage(err, t('payment.result.failed'))
    }
    appStore.showError(errorMessage.value)
  } finally {
    submitting.value = false
  }
}

async function loadCheckoutData() {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await paymentAPI.getCheckoutInfo()
    checkout.value = res.data
    if (enabledMethods.value.length) {
      const order: readonly string[] = METHOD_ORDER
      const sorted = [...enabledMethods.value].sort((a, b) => {
        const ai = order.indexOf(a)
        const bi = order.indexOf(b)
        return (ai === -1 ? 999 : ai) - (bi === -1 ? 999 : bi)
      })
      selectedMethod.value = sorted[0]
    }
    if (checkout.value.balance_disabled) {
      activeTab.value = 'subscription'
    }
    // Handle renewal navigation: ?tab=subscription&group=123
    if (route.query.tab === 'subscription') {
      activeTab.value = 'subscription'
      if (route.query.group) {
        const groupId = Number(route.query.group)
        const groupPlans = checkout.value.plans.filter(p => p.group_id === groupId)
        if (groupPlans.length === 1) {
          selectedPlan.value = groupPlans[0]
        } else if (groupPlans.length > 1) {
          renewGroupId.value = groupId
          showRenewalModal.value = true
        }
      }
    }
  } catch (err: unknown) { appStore.showError(extractApiErrorMessage(err, t('common.error'))) }
  finally { loading.value = false }
}

async function refreshCheckout() {
  await loadCheckoutData()
  subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
}

onMounted(async () => {
  await loadCheckoutData()
  // Fetch active subscriptions (uses cache, non-blocking)
  subscriptionStore.fetchActiveSubscriptions().catch(() => {})
})
</script>
