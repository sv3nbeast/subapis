<template>
  <section class="border-t border-gray-100 pt-6 dark:border-dark-700" data-testid="api-key-group-templates">
    <div class="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
      <div>
        <h3 class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t("admin.settings.apiKeyUsage.groupTemplates.title") }}
        </h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.apiKeyUsage.groupTemplates.description") }}
        </p>
      </div>

      <div class="flex w-full flex-col gap-2 sm:flex-row lg:w-auto">
        <select v-model.number="selectedGroupId" class="input min-w-64 text-sm">
          <option :value="0">{{ t("admin.settings.apiKeyUsage.groupTemplates.selectGroup") }}</option>
          <option v-for="group in availableGroups" :key="group.id" :value="group.id">
            #{{ group.id }} · {{ group.name }} ({{ group.platform }})
          </option>
        </select>
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="selectedGroupId <= 0"
          @click="addGroupTemplate"
        >
          {{ t("admin.settings.apiKeyUsage.groupTemplates.addGroup") }}
        </button>
      </div>
    </div>

    <div
      v-if="!modelValue.length"
      class="mt-4 border border-dashed border-gray-300 p-5 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
    >
      {{ t("admin.settings.apiKeyUsage.groupTemplates.empty") }}
    </div>

    <div
      v-for="(config, configIndex) in modelValue"
      :key="config.group_id"
      class="mt-4 border border-gray-200 dark:border-dark-600"
    >
      <div class="flex flex-col gap-3 border-b border-gray-100 p-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex min-w-0 items-center gap-3">
          <Toggle
            :model-value="config.enabled"
            @update:model-value="updateConfig(configIndex, { enabled: $event })"
          />
          <div class="min-w-0">
            <div class="truncate text-sm font-semibold text-gray-900 dark:text-white">
              {{ groupLabel(config.group_id) }}
            </div>
            <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ config.enabled
                ? t("admin.settings.apiKeyUsage.groupTemplates.overrideEnabled")
                : t("admin.settings.apiKeyUsage.groupTemplates.overrideDisabled") }}
            </div>
          </div>
        </div>
        <button type="button" class="text-sm text-red-600 hover:text-red-700 dark:text-red-400" @click="removeConfig(configIndex)">
          {{ t("common.delete") }}
        </button>
      </div>

      <div class="p-4">
        <div class="flex items-center justify-between gap-3">
          <div>
            <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t("admin.settings.apiKeyUsage.groupTemplates.templates") }}
            </div>
            <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t("admin.settings.apiKeyUsage.groupTemplates.templatesHint") }}
            </div>
          </div>
          <button type="button" class="btn btn-secondary btn-xs" @click="addTemplate(configIndex)">
            {{ t("admin.settings.apiKeyUsage.groupTemplates.addTemplate") }}
          </button>
        </div>

        <div
          v-if="!config.templates.length"
          class="mt-3 border border-dashed border-gray-300 p-4 text-center text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400"
        >
          {{ t("admin.settings.apiKeyUsage.groupTemplates.noTemplates") }}
        </div>

        <div
          v-for="(template, templateIndex) in config.templates"
          :key="`${template.id}-${templateIndex}`"
          class="mt-4 border-t border-gray-100 pt-4 first:border-t-0 first:pt-0 dark:border-dark-700"
        >
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div class="flex min-w-0 items-center gap-3">
              <Toggle
                :model-value="template.enabled"
                @update:model-value="updateTemplateVisibility(configIndex, templateIndex, $event)"
              />
              <div class="min-w-0">
                <div class="truncate text-sm font-medium text-gray-900 dark:text-white">
                  {{ template.label || template.id }}
                </div>
                <div class="font-mono text-xs text-gray-500 dark:text-gray-400">
                  {{ template.id }} · {{ template.kind }}
                </div>
              </div>
            </div>
            <button
              type="button"
              class="text-sm text-red-600 hover:text-red-700 dark:text-red-400"
              @click="removeTemplate(configIndex, templateIndex)"
            >
              {{ t("common.delete") }}
            </button>
          </div>

          <details class="mt-3">
            <summary class="cursor-pointer text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400">
              {{ t("admin.settings.apiKeyUsage.groupTemplates.editTemplate") }}
            </summary>
            <label class="mt-3 block text-sm text-gray-700 dark:text-gray-300">
              <span class="mb-1.5 block font-medium">
                {{ t("admin.settings.apiKeyUsage.groupTemplates.templateJson") }}
              </span>
              <textarea
                :value="templateDraft(configIndex, templateIndex, template)"
                rows="16"
                class="input min-h-72 font-mono text-xs"
                :class="templateError(configIndex, templateIndex) ? 'border-red-500 focus:border-red-500 focus:ring-red-500' : ''"
                @input="updateTemplateJSON(configIndex, templateIndex, inputValue($event))"
              ></textarea>
            </label>
            <p v-if="templateError(configIndex, templateIndex)" class="mt-1.5 text-xs text-red-600 dark:text-red-400">
              {{ templateError(configIndex, templateIndex) }}
            </p>
          </details>
        </div>

        <p class="mt-4 text-xs text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.apiKeyUsage.groupTemplates.variables") }}
        </p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import Toggle from "@/components/common/Toggle.vue";
import type {
  AdminGroup,
  APIKeyUsageClientTemplate,
  APIKeyUsageGroupTemplate,
} from "@/types";

const props = defineProps<{
  modelValue: APIKeyUsageGroupTemplate[];
  groups: AdminGroup[];
}>();

const emit = defineEmits<{
  (event: "update:modelValue", value: APIKeyUsageGroupTemplate[]): void;
  (event: "validity", value: boolean): void;
}>();

const { t } = useI18n();
const selectedGroupId = ref(0);
const drafts = ref<Record<string, string>>({});
const errors = ref<Record<string, string>>({});

const configuredGroupIDs = computed(() => new Set(props.modelValue.map((config) => config.group_id)));
const availableGroups = computed(() => props.groups.filter((group) => !configuredGroupIDs.value.has(group.id)));

watch(availableGroups, (groups) => {
  if (selectedGroupId.value && !groups.some((group) => group.id === selectedGroupId.value)) {
    selectedGroupId.value = 0;
  }
});

watch(
  () => props.modelValue,
  (configs) => {
    const nextDrafts: Record<string, string> = {};
    const nextErrors: Record<string, string> = {};
    configs.forEach((config, configIndex) => {
      config.templates.forEach((template, templateIndex) => {
        const key = draftKey(configIndex, templateIndex);
        nextDrafts[key] = errors.value[key] ? drafts.value[key] : JSON.stringify(template, null, 2);
        if (errors.value[key]) nextErrors[key] = errors.value[key];
      });
    });
    drafts.value = nextDrafts;
    errors.value = nextErrors;
    emitValidity();
  },
  { deep: true, immediate: true },
);

function draftKey(configIndex: number, templateIndex: number): string {
  const config = props.modelValue[configIndex];
  const template = config?.templates[templateIndex];
  return `${config?.group_id ?? configIndex}:${template?.id ?? "template"}:${templateIndex}`;
}

function inputValue(event: Event): string {
  return (event.target as HTMLTextAreaElement).value;
}

function groupLabel(groupID: number): string {
  const group = props.groups.find((item) => item.id === groupID);
  return group ? `#${group.id} · ${group.name} (${group.platform})` : `#${groupID}`;
}

function groupPlatform(groupID: number): string {
  return props.groups.find((item) => item.id === groupID)?.platform || "custom";
}

function modelEntries(models: string[]): Record<string, { name: string }> {
  return Object.fromEntries(models.map((model) => [model, { name: model }]));
}

function defaultModels(groupID: number): Record<string, unknown> {
  const group = props.groups.find((item) => item.id === groupID);
  const configuredModels = group?.models_list_config?.enabled
    ? group.models_list_config.models.filter(Boolean)
    : [];
  if (configuredModels.length) return modelEntries(configuredModels);

  switch (group?.platform) {
    case "grok":
      return {
        "grok-4.5": { name: "Grok 4.5" },
        "grok-4.3": { name: "Grok 4.3" },
        "grok-build-0.1": { name: "Grok Build 0.1" },
        "grok-composer-2.5-fast": { name: "Grok Composer 2.5 Fast" },
        "grok-4.20-0309-reasoning": { name: "Grok 4.20 Reasoning", reasoning: true },
        "grok-4.20-0309-non-reasoning": { name: "Grok 4.20 Non Reasoning" },
        "grok-4.20-multi-agent-0309": { name: "Grok 4.20 Multi Agent" },
      };
    case "anthropic":
      return {
        "claude-fable-5": { name: "Claude Fable 5", limit: { context: 1048576, output: 128000 } },
        "claude-sonnet-5": { name: "Claude Sonnet 5", limit: { context: 1000000, output: 128000 } },
        "claude-opus-4-8": { name: "Claude Opus 4.8", limit: { context: 200000, output: 128000 } },
        "claude-opus-4-8-thinking": { name: "Claude Opus 4.8 (Thinking)", limit: { context: 200000, output: 128000 }, options: { thinking: { type: "enabled", budgetTokens: 24576 } } },
        "claude-opus-4-7": { name: "Claude Opus 4.7", limit: { context: 200000, output: 128000 } },
        "claude-opus-4-6": { name: "Claude Opus 4.6", limit: { context: 200000, output: 128000 } },
        "claude-opus-4-6-thinking": { name: "Claude Opus 4.6 (Thinking)", limit: { context: 200000, output: 128000 }, options: { thinking: { type: "enabled", budgetTokens: 24576 } } },
        "claude-sonnet-4-6": { name: "Claude Sonnet 4.6", limit: { context: 200000, output: 64000 } },
        "claude-sonnet-4-6-thinking": { name: "Claude Sonnet 4.6 (Thinking)", limit: { context: 200000, output: 64000 }, options: { thinking: { type: "enabled", budgetTokens: 24576 } } },
        "claude-opus-4-5-20251101": { name: "Claude Opus 4.5", limit: { context: 200000, output: 64000 } },
        "claude-opus-4-5-thinking": { name: "Claude Opus 4.5 (Thinking)", limit: { context: 200000, output: 64000 }, options: { thinking: { type: "enabled", budgetTokens: 24576 } } },
        "claude-sonnet-4-5": { name: "Claude Sonnet 4.5", limit: { context: 200000, output: 64000 } },
        "claude-sonnet-4-5-thinking": { name: "Claude Sonnet 4.5 (Thinking)", limit: { context: 200000, output: 64000 }, options: { thinking: { type: "enabled", budgetTokens: 24576 } } },
        "claude-haiku-4-5": { name: "Claude Haiku 4.5", limit: { context: 200000, output: 64000 } },
      };
    case "openai":
      return modelEntries(["gpt-5.6-sol", "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark"]);
    case "gemini":
      return modelEntries(["gemini-3.5-flash", "gemini-3.1-pro-preview", "gemini-3-flash-preview", "gemini-2.5-pro", "gemini-2.5-flash"]);
    default:
      return {};
  }
}

function defaultOpenCodeContent(groupID: number): string {
  const platform = groupPlatform(groupID);
  const providerID = platform === "gemini" ? "gemini" : platform;
  const npm = platform === "anthropic"
    ? "@ai-sdk/anthropic"
    : platform === "gemini"
      ? "@ai-sdk/google"
      : platform === "openai"
        ? ""
        : "@ai-sdk/openai-compatible";
  const baseURL = platform === "gemini" ? "{{base_url_v1beta}}" : "{{base_url_v1}}";
  return JSON.stringify({
    $schema: "https://opencode.ai/config.json",
    provider: {
      [providerID]: {
        ...(npm ? { npm } : {}),
        options: {
          baseURL,
          apiKey: "{{api_key}}",
        },
        models: defaultModels(groupID),
      },
    },
  }, null, 2);
}

function createTemplate(groupID: number, existing: APIKeyUsageClientTemplate[]): APIKeyUsageClientTemplate {
  const existingIDs = new Set(existing.map((template) => template.id));
  let id = "opencode";
  let suffix = 2;
  while (existingIDs.has(id)) {
    id = `template-${suffix}`;
    suffix += 1;
  }
  const sortOrder = existing.reduce((highest, template) => Math.max(highest, template.sort_order), 0) + 10;
  return {
    id,
    label: id === "opencode" ? "OpenCode" : t("admin.settings.apiKeyUsage.groupTemplates.newTemplate"),
    kind: "opencode",
    enabled: true,
    sort_order: sortOrder,
    variants: [{
      id: "default",
      label: t("admin.settings.apiKeyUsage.groupTemplates.defaultVariant"),
      files: [{
        path: "opencode.json",
        content: defaultOpenCodeContent(groupID),
      }],
    }],
  };
}

function addGroupTemplate() {
  if (selectedGroupId.value <= 0 || configuredGroupIDs.value.has(selectedGroupId.value)) return;
  const groupID = selectedGroupId.value;
  emit("update:modelValue", [
    ...props.modelValue,
    {
      group_id: groupID,
      enabled: true,
      templates: [createTemplate(groupID, [])],
    },
  ]);
  selectedGroupId.value = 0;
}

function updateConfig(index: number, patch: Partial<APIKeyUsageGroupTemplate>) {
  emit("update:modelValue", props.modelValue.map((config, current) => (
    current === index ? { ...config, ...patch } : config
  )));
}

function removeConfig(index: number) {
  emit("update:modelValue", props.modelValue.filter((_, current) => current !== index));
}

function addTemplate(configIndex: number) {
  const config = props.modelValue[configIndex];
  updateConfig(configIndex, {
    templates: [...config.templates, createTemplate(config.group_id, config.templates)],
  });
}

function updateTemplate(configIndex: number, templateIndex: number, template: APIKeyUsageClientTemplate) {
  const templates = props.modelValue[configIndex].templates.map((item, current) => (
    current === templateIndex ? template : item
  ));
  updateConfig(configIndex, { templates });
}

function updateTemplateVisibility(configIndex: number, templateIndex: number, enabled: boolean) {
  const current = props.modelValue[configIndex].templates[templateIndex];
  updateTemplate(configIndex, templateIndex, { ...current, enabled });
}

function removeTemplate(configIndex: number, templateIndex: number) {
  updateConfig(configIndex, {
    templates: props.modelValue[configIndex].templates.filter((_, current) => current !== templateIndex),
  });
}

function templateDraft(configIndex: number, templateIndex: number, template: APIKeyUsageClientTemplate): string {
  return drafts.value[draftKey(configIndex, templateIndex)] ?? JSON.stringify(template, null, 2);
}

function templateError(configIndex: number, templateIndex: number): string {
  return errors.value[draftKey(configIndex, templateIndex)] || "";
}

function isValidTemplateShape(value: unknown): value is APIKeyUsageClientTemplate {
  if (!value || Array.isArray(value) || typeof value !== "object") return false;
  const template = value as Partial<APIKeyUsageClientTemplate>;
  if (!template.id || !template.label || !template.kind || typeof template.enabled !== "boolean") return false;
  if (!Number.isFinite(template.sort_order) || !Array.isArray(template.variants) || !template.variants.length) return false;
  return template.variants.every((variant) => (
    Boolean(variant?.id)
    && Boolean(variant?.label)
    && Array.isArray(variant?.files)
    && variant.files.length > 0
    && variant.files.every((file) => Boolean(file?.path) && Boolean(file?.content))
  ));
}

function updateTemplateJSON(configIndex: number, templateIndex: number, value: string) {
  const key = draftKey(configIndex, templateIndex);
  drafts.value[key] = value;
  try {
    const parsed: unknown = JSON.parse(value);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
      throw new Error(t("admin.settings.apiKeyUsage.groupTemplates.objectRequired"));
    }
    if (!isValidTemplateShape(parsed)) {
      throw new Error(t("admin.settings.apiKeyUsage.groupTemplates.fieldsRequired"));
    }
    delete errors.value[key];
    updateTemplate(configIndex, templateIndex, parsed);
  } catch (error) {
    errors.value[key] = error instanceof Error ? error.message : String(error);
    emitValidity();
  }
}

function emitValidity() {
  emit("validity", Object.values(errors.value).every((error) => !error));
}
</script>
