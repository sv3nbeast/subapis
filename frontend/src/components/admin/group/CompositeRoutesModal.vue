<template>
  <BaseDialog
    :show="show"
    :title="dialogTitle"
    width="extra-wide"
    @close="handleClose"
  >
    <div
      v-if="group"
      class="grid gap-5 lg:grid-cols-[minmax(0,1.15fr)_minmax(320px,0.85fr)]"
    >
      <section class="min-w-0">
        <div class="mb-3 flex items-center justify-between gap-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t("admin.groups.compositeRoutes.routes") }}
          </h3>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="loading"
            :title="t('common.refresh')"
            @click="loadRoutes"
          >
            <Icon
              name="refresh"
              size="sm"
              :class="loading ? 'animate-spin' : ''"
            />
          </button>
        </div>

        <div
          class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600"
        >
          <div
            v-if="loading"
            class="flex h-36 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
          >
            {{ t("common.loading") }}
          </div>
          <div
            v-else-if="routes.length === 0"
            class="flex h-36 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
          >
            {{ t("admin.groups.compositeRoutes.empty") }}
          </div>
          <div v-else class="overflow-x-auto">
            <table
              class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600"
            >
              <thead
                class="bg-gray-50 text-left text-xs font-medium uppercase text-gray-500 dark:bg-dark-800 dark:text-gray-400"
              >
                <tr>
                  <th class="px-3 py-2">
                    {{ t("admin.groups.compositeRoutes.publicModel") }}
                  </th>
                  <th class="px-3 py-2">
                    {{ t("admin.groups.compositeRoutes.target") }}
                  </th>
                  <th class="px-3 py-2">
                    {{ t("admin.groups.compositeRoutes.scope") }}
                  </th>
                  <th class="px-3 py-2 text-right">
                    {{ t("admin.groups.columns.actions") }}
                  </th>
                </tr>
              </thead>
              <tbody
                class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-900"
              >
                <tr
                  v-for="route in routes"
                  :key="route.id"
                  :class="!route.enabled && 'opacity-60'"
                >
                  <td class="max-w-[15rem] px-3 py-2">
                    <div
                      class="break-all font-medium text-gray-900 dark:text-white"
                    >
                      {{ route.public_model }}
                    </div>
                    <div class="mt-1 flex flex-wrap items-center gap-1.5">
                      <span class="badge badge-gray">{{
                        matchTypeLabel(route.match_type)
                      }}</span>
                      <span v-if="!route.enabled" class="badge badge-danger">
                        {{ t("admin.accounts.status.inactive") }}
                      </span>
                    </div>
                  </td>
                  <td class="px-3 py-2">
                    <div
                      class="flex items-center gap-1.5 text-gray-900 dark:text-white"
                    >
                      <PlatformIcon
                        :platform="route.target_platform"
                        size="xs"
                      />
                      <span>{{ platformLabel(route.target_platform) }}</span>
                    </div>
                    <div
                      class="mt-1 break-all text-xs text-gray-500 dark:text-gray-400"
                    >
                      {{ route.upstream_model || route.public_model }}
                    </div>
                  </td>
                  <td class="px-3 py-2">
                    <div class="text-gray-700 dark:text-gray-300">
                      {{ endpointLabel(route.endpoint) }}
                    </div>
                    <div class="text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.groups.compositeRoutes.priority") }}:
                      {{ route.priority }}
                    </div>
                  </td>
                  <td class="px-3 py-2">
                    <div class="flex justify-end gap-1">
                      <button
                        type="button"
                        class="rounded p-1.5 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                        :title="t('common.edit')"
                        :data-testid="`composite-route-edit-${route.id}`"
                        @click="editRoute(route)"
                      >
                        <Icon name="edit" size="sm" />
                      </button>
                      <button
                        type="button"
                        class="rounded p-1.5 text-gray-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                        :title="t('common.delete')"
                        :data-testid="`composite-route-delete-${route.id}`"
                        @click="deleteRoute(route)"
                      >
                        <Icon name="trash" size="sm" />
                      </button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </section>

      <section class="space-y-5">
        <form
          class="space-y-3"
          data-testid="composite-route-form"
          @submit.prevent="saveRoute"
        >
          <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{
                editingID
                  ? t("admin.groups.compositeRoutes.editRoute")
                  : t("admin.groups.compositeRoutes.addRoute")
              }}
            </h3>
            <button
              v-if="editingID"
              type="button"
              class="text-xs font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
              @click="resetForm"
            >
              {{ t("common.cancel") }}
            </button>
          </div>

          <div>
            <label class="input-label">{{
              t("admin.groups.compositeRoutes.publicModel")
            }}</label>
            <input
              v-model.trim="form.public_model"
              data-testid="composite-public-model"
              type="text"
              class="input"
              required
              placeholder="openrouter/gpt-5"
            />
          </div>

          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label class="input-label">{{
                t("admin.groups.compositeRoutes.matchType")
              }}</label>
              <Select v-model="form.match_type" :options="matchTypeOptions" />
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.compositeRoutes.endpoint")
              }}</label>
              <Select v-model="form.endpoint" :options="endpointOptions" />
            </div>
          </div>

          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label class="input-label">{{
                t("admin.groups.compositeRoutes.targetPlatform")
              }}</label>
              <Select
                v-model="form.target_platform"
                :options="platformOptions"
              />
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.compositeRoutes.priority")
              }}</label>
              <input
                v-model.number="form.priority"
                type="number"
                min="1"
                step="1"
                class="input"
              />
            </div>
          </div>

          <div>
            <label class="input-label">{{
              t("admin.groups.compositeRoutes.upstreamModel")
            }}</label>
            <input
              v-model.trim="form.upstream_model"
              type="text"
              class="input"
              placeholder="gpt-5"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t("admin.groups.compositeRoutes.upstreamModelHint") }}
            </p>
          </div>

          <div>
            <label class="input-label">{{
              t("admin.groups.compositeRoutes.notes")
            }}</label>
            <textarea
              v-model.trim="form.notes"
              rows="2"
              class="input"
            ></textarea>
          </div>

          <div class="flex items-center justify-between gap-3">
            <label
              class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"
            >
              <input
                v-model="form.enabled"
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-700"
              />
              {{ t("admin.groups.compositeRoutes.enabled") }}
            </label>
            <button type="submit" class="btn btn-primary" :disabled="saving">
              <Icon v-if="!saving" name="check" size="sm" class="mr-2" />
              {{ editingID ? t("common.update") : t("common.create") }}
            </button>
          </div>
        </form>

        <div class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">
            {{ t("admin.groups.compositeRoutes.preview") }}
          </h3>
          <div class="space-y-3">
            <input
              v-model.trim="previewModel"
              data-testid="composite-preview-model"
              type="text"
              class="input"
              placeholder="openrouter/gpt-5"
              @keyup.enter="previewRoute"
            />
            <div class="flex gap-2">
              <Select
                v-model="previewEndpoint"
                :options="endpointOptions"
                class="min-w-0 flex-1"
              />
              <button
                type="button"
                class="btn btn-secondary"
                data-testid="composite-preview-submit"
                :disabled="previewing || !previewModel"
                :title="t('admin.groups.compositeRoutes.preview')"
                @click="previewRoute"
              >
                <Icon name="play" size="sm" />
              </button>
            </div>

            <div
              v-if="previewDecision"
              class="rounded-lg border border-gray-200 bg-gray-50 p-3 text-sm dark:border-dark-600 dark:bg-dark-800"
              data-testid="composite-preview-result"
            >
              <div class="mb-2 flex items-center gap-2">
                <span
                  :class="[
                    'badge',
                    previewDecision.matched ? 'badge-success' : 'badge-danger',
                  ]"
                >
                  {{
                    previewDecision.matched
                      ? t("admin.groups.compositeRoutes.matched")
                      : t("admin.groups.compositeRoutes.notMatched")
                  }}
                </span>
                <span class="badge badge-gray">{{
                  sourceLabel(previewDecision.source)
                }}</span>
              </div>
              <div
                v-if="previewDecision.matched"
                class="space-y-1 text-gray-700 dark:text-gray-300"
              >
                <div>
                  {{ t("admin.groups.compositeRoutes.targetPlatform") }}:
                  {{ platformLabel(previewDecision.target_platform) }}
                </div>
                <div class="break-all">
                  {{ t("admin.groups.compositeRoutes.upstreamModel") }}:
                  {{ previewDecision.upstream_model }}
                </div>
              </div>
              <div v-else class="text-gray-500 dark:text-gray-400">
                {{ previewDecision.reason }}
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t("common.close") }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import { adminAPI } from "@/api/admin";
import BaseDialog from "@/components/common/BaseDialog.vue";
import PlatformIcon from "@/components/common/PlatformIcon.vue";
import Select from "@/components/common/Select.vue";
import Icon from "@/components/icons/Icon.vue";
import { useAppStore } from "@/stores/app";
import { COMPOSITE_ROUTE_PLATFORM_OPTIONS } from "@/constants/platforms";
import type {
  AdminGroup,
  CompositeModelRoute,
  CompositeModelRouteInput,
  CompositeRouteDecision,
  CompositeRouteEndpoint,
  CompositeRouteMatchType,
  GroupPlatform,
} from "@/types";
import { extractApiErrorMessage } from "@/utils/apiError";

type ConcreteGroupPlatform = Exclude<GroupPlatform, "composite">;

interface CompositeRouteFormState {
  public_model: string;
  match_type: CompositeRouteMatchType;
  target_platform: ConcreteGroupPlatform;
  upstream_model: string;
  endpoint: CompositeRouteEndpoint;
  priority: number;
  enabled: boolean;
  notes: string;
}

const props = defineProps<{
  show: boolean;
  group: AdminGroup | null;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { t } = useI18n();
const appStore = useAppStore();

const routes = ref<CompositeModelRoute[]>([]);
const loading = ref(false);
const saving = ref(false);
const editingID = ref<number | null>(null);
const previewModel = ref("");
const previewEndpoint = ref<CompositeRouteEndpoint>("any");
const previewing = ref(false);
const previewDecision = ref<CompositeRouteDecision | null>(null);
let loadRequestID = 0;
let modalGeneration = 0;

const form = reactive<CompositeRouteFormState>({
  public_model: "",
  match_type: "exact",
  target_platform: "openai",
  upstream_model: "",
  endpoint: "any",
  priority: 100,
  enabled: true,
  notes: "",
});

const dialogTitle = computed(() =>
  props.group
    ? t("admin.groups.compositeRoutes.titleWithGroup", {
        name: props.group.name,
      })
    : t("admin.groups.compositeRoutes.title"),
);

const platformOptions = COMPOSITE_ROUTE_PLATFORM_OPTIONS;

const endpointOptions = computed(() => [
  { value: "any", label: t("admin.groups.compositeRoutes.endpoints.any") },
  {
    value: "messages",
    label: t("admin.groups.compositeRoutes.endpoints.messages"),
  },
  {
    value: "count_tokens",
    label: t("admin.groups.compositeRoutes.endpoints.countTokens"),
  },
  {
    value: "responses",
    label: t("admin.groups.compositeRoutes.endpoints.responses"),
  },
  {
    value: "chat_completions",
    label: t("admin.groups.compositeRoutes.endpoints.chatCompletions"),
  },
  {
    value: "embeddings",
    label: t("admin.groups.compositeRoutes.endpoints.embeddings"),
  },
  {
    value: "images",
    label: t("admin.groups.compositeRoutes.endpoints.images"),
  },
  {
    value: "gemini",
    label: t("admin.groups.compositeRoutes.endpoints.gemini"),
  },
]);

const matchTypeOptions = computed(() => [
  { value: "exact", label: t("admin.groups.compositeRoutes.match.exact") },
  { value: "prefix", label: t("admin.groups.compositeRoutes.match.prefix") },
]);

function resetForm() {
  editingID.value = null;
  form.public_model = "";
  form.match_type = "exact";
  form.target_platform = "openai";
  form.upstream_model = "";
  form.endpoint = "any";
  form.priority = 100;
  form.enabled = true;
  form.notes = "";
}

function resetModal() {
  modalGeneration += 1;
  loadRequestID += 1;
  loading.value = false;
  saving.value = false;
  previewing.value = false;
  routes.value = [];
  previewModel.value = "";
  previewEndpoint.value = "any";
  previewDecision.value = null;
  resetForm();
}

function matchTypeLabel(matchType: CompositeRouteMatchType) {
  return (
    matchTypeOptions.value.find((option) => option.value === matchType)
      ?.label || matchType
  );
}

function endpointLabel(endpoint: CompositeRouteEndpoint) {
  return (
    endpointOptions.value.find((option) => option.value === endpoint)?.label ||
    endpoint
  );
}

function platformLabel(platform: string) {
  return platform ? t(`admin.groups.platforms.${platform}`) : "-";
}

function sourceLabel(source: string) {
  if (source === "route")
    return t("admin.groups.compositeRoutes.sources.route");
  if (source === "detector")
    return t("admin.groups.compositeRoutes.sources.detector");
  return source || "-";
}

function toRouteInput(): CompositeModelRouteInput {
  return {
    public_model: form.public_model.trim(),
    match_type: form.match_type,
    target_platform: form.target_platform,
    upstream_model: form.upstream_model.trim(),
    endpoint: form.endpoint,
    priority: Number(form.priority) || 100,
    enabled: form.enabled,
    notes: form.notes.trim(),
  };
}

async function loadRoutes() {
  const groupID = props.group?.id;
  if (!groupID) return;
  const requestID = ++loadRequestID;
  loading.value = true;
  try {
    const result = await adminAPI.groups.listCompositeRoutes(groupID);
    if (
      requestID !== loadRequestID ||
      props.group?.id !== groupID ||
      !props.show
    )
      return;
    routes.value = [...result].sort(
      (a, b) => a.priority - b.priority || a.id - b.id,
    );
  } catch (error: unknown) {
    if (requestID === loadRequestID && props.show) {
      appStore.showError(
        extractApiErrorMessage(
          error,
          t("admin.groups.compositeRoutes.failedToLoad"),
        ),
      );
    }
  } finally {
    if (requestID === loadRequestID) loading.value = false;
  }
}

function editRoute(route: CompositeModelRoute) {
  editingID.value = route.id;
  form.public_model = route.public_model;
  form.match_type = route.match_type;
  form.target_platform = route.target_platform;
  form.upstream_model = route.upstream_model;
  form.endpoint = route.endpoint;
  form.priority = route.priority || 100;
  form.enabled = route.enabled;
  form.notes = route.notes || "";
}

async function saveRoute() {
  const groupID = props.group?.id;
  if (!groupID || !form.public_model.trim()) {
    if (groupID)
      appStore.showError(t("admin.groups.compositeRoutes.publicModelRequired"));
    return;
  }

  const generation = modalGeneration;
  saving.value = true;
  try {
    const payload = toRouteInput();
    let successKey: string;
    if (editingID.value) {
      await adminAPI.groups.updateCompositeRoute(
        groupID,
        editingID.value,
        payload,
      );
      successKey = "admin.groups.compositeRoutes.routeUpdated";
    } else {
      await adminAPI.groups.createCompositeRoute(groupID, payload);
      successKey = "admin.groups.compositeRoutes.routeCreated";
    }
    if (generation !== modalGeneration) return;
    appStore.showSuccess(t(successKey));
    resetForm();
    await loadRoutes();
  } catch (error: unknown) {
    if (generation === modalGeneration) {
      appStore.showError(
        extractApiErrorMessage(
          error,
          t("admin.groups.compositeRoutes.failedToSave"),
        ),
      );
    }
  } finally {
    if (generation === modalGeneration) saving.value = false;
  }
}

async function deleteRoute(route: CompositeModelRoute) {
  const groupID = props.group?.id;
  if (
    !groupID ||
    !window.confirm(t("admin.groups.compositeRoutes.deleteConfirm"))
  )
    return;

  const generation = modalGeneration;
  try {
    await adminAPI.groups.deleteCompositeRoute(groupID, route.id);
    if (generation !== modalGeneration) return;
    if (editingID.value === route.id) resetForm();
    appStore.showSuccess(t("admin.groups.compositeRoutes.routeDeleted"));
    await loadRoutes();
  } catch (error: unknown) {
    if (generation === modalGeneration) {
      appStore.showError(
        extractApiErrorMessage(
          error,
          t("admin.groups.compositeRoutes.failedToDelete"),
        ),
      );
    }
  }
}

async function previewRoute() {
  const groupID = props.group?.id;
  if (!groupID || !previewModel.value.trim()) return;

  const generation = modalGeneration;
  previewing.value = true;
  try {
    const decision = await adminAPI.groups.previewCompositeRoute(
      groupID,
      {
        model: previewModel.value.trim(),
        endpoint: previewEndpoint.value,
      },
    );
    if (generation === modalGeneration) previewDecision.value = decision;
  } catch (error: unknown) {
    if (generation === modalGeneration) {
      appStore.showError(
        extractApiErrorMessage(
          error,
          t("admin.groups.compositeRoutes.failedToPreview"),
        ),
      );
    }
  } finally {
    if (generation === modalGeneration) previewing.value = false;
  }
}

function handleClose() {
  emit("close");
}

watch(
  () => [props.show, props.group?.id] as const,
  ([show, groupID]) => {
    resetModal();
    if (show && groupID) void loadRoutes();
  },
  { immediate: true },
);
</script>
