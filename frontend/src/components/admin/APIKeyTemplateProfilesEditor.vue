<template>
  <div class="border-t border-gray-100 pt-6 dark:border-dark-700" data-testid="api-key-template-profiles">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div>
        <h3 class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t("admin.settings.apiKeyUsage.templateProfiles.title") }}
        </h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.apiKeyUsage.templateProfiles.description") }}
        </p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" @click="addProfile">
        {{ t("admin.settings.apiKeyUsage.templateProfiles.addProfile") }}
      </button>
    </div>

    <div v-if="!modelValue.length" class="mt-4 rounded-lg border border-dashed border-gray-300 p-5 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
      {{ t("admin.settings.apiKeyUsage.templateProfiles.empty") }}
    </div>

    <div v-for="(profile, index) in modelValue" :key="`${profile.id}-${index}`" class="mt-4 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
      <div class="flex items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <Toggle :model-value="profile.enabled" @update:model-value="updateProfile(index, { enabled: $event })" />
          <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ profile.name || profile.id }}</span>
        </div>
        <button type="button" class="text-sm text-red-600 hover:text-red-700 dark:text-red-400" @click="removeProfile(index)">
          {{ t("common.delete") }}
        </button>
      </div>

      <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2">
        <label class="block text-sm text-gray-700 dark:text-gray-300">
          <span class="mb-1.5 block font-medium">{{ t("admin.settings.apiKeyUsage.templateProfiles.profileId") }}</span>
          <input :value="profile.id" class="input font-mono text-sm" type="text" @input="updateProfile(index, { id: inputValue($event) })" />
        </label>
        <label class="block text-sm text-gray-700 dark:text-gray-300">
          <span class="mb-1.5 block font-medium">{{ t("admin.settings.apiKeyUsage.templateProfiles.profileName") }}</span>
          <input :value="profile.name" class="input text-sm" type="text" @input="updateProfile(index, { name: inputValue($event) })" />
        </label>
        <label class="block text-sm text-gray-700 dark:text-gray-300">
          <span class="mb-1.5 block font-medium">{{ t("admin.settings.apiKeyUsage.templateProfiles.mode") }}</span>
          <select :value="profile.mode" class="input text-sm" @change="updateProfile(index, { mode: inputValue($event) as APIKeyUsageTemplateProfile['mode'] })">
            <option value="append">append</option>
            <option value="replace">replace</option>
          </select>
        </label>
        <label class="block text-sm text-gray-700 dark:text-gray-300">
          <span class="mb-1.5 block font-medium">{{ t("admin.settings.apiKeyUsage.templateProfiles.priority") }}</span>
          <input :value="profile.priority" class="input font-mono text-sm" type="number" @input="updateProfile(index, { priority: numberValue($event) })" />
        </label>
        <label class="block text-sm text-gray-700 dark:text-gray-300">
          <span class="mb-1.5 block font-medium">{{ t("admin.settings.apiKeyUsage.templateProfiles.platforms") }}</span>
          <input
            :value="profile.match.platforms.join(', ')"
            class="input font-mono text-sm"
            type="text"
            :placeholder="t('admin.settings.apiKeyUsage.templateProfiles.platformsPlaceholder')"
            @input="updateMatch(index, { platforms: commaValues(inputValue($event)) })"
          />
        </label>
        <label class="block text-sm text-gray-700 dark:text-gray-300">
          <span class="mb-1.5 block font-medium">claude_code_only</span>
          <select
            :value="profile.match.claude_code_only"
            class="input text-sm"
            @change="updateMatch(index, { claude_code_only: inputValue($event) as APIKeyUsageTemplateMatch['claude_code_only'] })"
          >
            <option value="any">any</option>
            <option value="required">required</option>
            <option value="forbidden">forbidden</option>
          </select>
        </label>
      </div>

      <label class="mt-4 block text-sm text-gray-700 dark:text-gray-300">
        <span class="mb-1.5 block font-medium">{{ t("admin.settings.apiKeyUsage.templateProfiles.groups") }}</span>
        <select
          :value="profile.match.group_ids.map(String)"
          multiple
          class="input min-h-28 text-sm"
          @change="updateGroupIds(index, $event)"
        >
          <option v-for="group in groups" :key="group.id" :value="String(group.id)">
            #{{ group.id }} · {{ group.name }} ({{ group.platform }})<template v-if="group.claude_code_only"> · Claude Code only</template>
          </option>
        </select>
        <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.apiKeyUsage.templateProfiles.matchHint") }}
        </span>
      </label>

      <div class="mt-4">
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t("admin.settings.apiKeyUsage.templateProfiles.presets") }}</span>
          <button v-for="preset in presetButtons" :key="preset.id" type="button" class="btn btn-secondary btn-xs" @click="addPreset(index, preset.id)">
            {{ preset.label }}
          </button>
        </div>
        <label class="mt-3 block text-sm text-gray-700 dark:text-gray-300">
          <span class="mb-1.5 block font-medium">{{ t("admin.settings.apiKeyUsage.templateProfiles.templatesJson") }}</span>
          <textarea
            :value="templateDrafts[index]"
            rows="14"
            class="input min-h-64 font-mono text-xs"
            :class="templateErrors[index] ? 'border-red-500 focus:border-red-500 focus:ring-red-500' : ''"
            @input="updateTemplates(index, inputValue($event))"
          ></textarea>
        </label>
        <p v-if="templateErrors[index]" class="mt-1.5 text-xs text-red-600 dark:text-red-400">
          {{ templateErrors[index] }}
        </p>
        <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.apiKeyUsage.templateProfiles.variables") }}
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import Toggle from "@/components/common/Toggle.vue";
import type {
  AdminGroup,
  APIKeyUsageClientTemplate,
  APIKeyUsageTemplateMatch,
  APIKeyUsageTemplateProfile,
} from "@/types";

const props = defineProps<{
  modelValue: APIKeyUsageTemplateProfile[];
  groups: AdminGroup[];
}>();

const emit = defineEmits<{
  (event: "update:modelValue", value: APIKeyUsageTemplateProfile[]): void;
  (event: "validity", value: boolean): void;
}>();

const { t } = useI18n();
const templateDrafts = ref<string[]>([]);
const templateErrors = ref<string[]>([]);

watch(
  () => props.modelValue,
  (profiles) => {
    templateDrafts.value = profiles.map((profile) => JSON.stringify(profile.templates, null, 2));
    templateErrors.value = profiles.map(() => "");
    emit("validity", true);
  },
  { immediate: true },
);

function inputValue(event: Event): string {
  return (event.target as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement).value;
}

function numberValue(event: Event): number {
  const value = Number(inputValue(event));
  return Number.isFinite(value) ? Math.trunc(value) : 0;
}

function commaValues(value: string): string[] {
  return [...new Set(value.split(",").map((item) => item.trim().toLowerCase()).filter(Boolean))];
}

function updateProfile(index: number, patch: Partial<APIKeyUsageTemplateProfile>) {
  const profiles = props.modelValue.map((profile, current) => current === index ? { ...profile, ...patch } : profile);
  emit("update:modelValue", profiles);
}

function updateMatch(index: number, patch: Partial<APIKeyUsageTemplateMatch>) {
  updateProfile(index, { match: { ...props.modelValue[index].match, ...patch } });
}

function updateGroupIds(index: number, event: Event) {
  const select = event.target as HTMLSelectElement;
  updateMatch(index, { group_ids: [...select.selectedOptions].map((option) => Number(option.value)) });
}

function addProfile() {
  const suffix = Date.now().toString(36);
  const profile: APIKeyUsageTemplateProfile = {
    id: `profile-${suffix}`,
    name: t("admin.settings.apiKeyUsage.templateProfiles.newProfile"),
    enabled: true,
    priority: 100,
    mode: "append",
    match: { platforms: [], group_ids: [], claude_code_only: "any" },
    templates: [],
  };
  emit("update:modelValue", [...props.modelValue, profile]);
}

function removeProfile(index: number) {
  emit("update:modelValue", props.modelValue.filter((_, current) => current !== index));
}

function updateTemplates(index: number, value: string) {
  templateDrafts.value[index] = value;
  try {
    const parsed: unknown = JSON.parse(value);
    if (!Array.isArray(parsed)) throw new Error(t("admin.settings.apiKeyUsage.templateProfiles.arrayRequired"));
    templateErrors.value[index] = "";
    updateProfile(index, { templates: parsed as APIKeyUsageClientTemplate[] });
  } catch (error) {
    templateErrors.value[index] = error instanceof Error ? error.message : String(error);
    emit("validity", false);
    return;
  }
  emit("validity", templateErrors.value.every((error) => !error));
}

const presets: Record<string, APIKeyUsageClientTemplate> = {
  claude: {
    id: "custom-claude-code", label: "Claude Code", kind: "claude_code", enabled: true, sort_order: 100,
    description: "Claude Code", note: "", variants: [
      { id: "unix", label: "macOS / Linux", files: [{ path: "Terminal", content: "export ANTHROPIC_BASE_URL=\"{{base_url}}\"\nexport ANTHROPIC_AUTH_TOKEN=\"{{api_key}}\"" }] },
      { id: "powershell", label: "PowerShell", files: [{ path: "PowerShell", content: "$env:ANTHROPIC_BASE_URL=\"{{base_url}}\"\n$env:ANTHROPIC_AUTH_TOKEN=\"{{api_key}}\"" }] },
    ],
  },
  codex: {
    id: "custom-codex-http", label: "Codex HTTP", kind: "codex", enabled: true, sort_order: 110, variants: [
      { id: "unix", label: "macOS / Linux", files: [
        { path: "~/.codex/config.toml", content: "model_provider = \"Sub2API\"\nmodel = \"{{codex_model}}\"\n\n[model_providers.Sub2API]\nbase_url = \"{{base_url_v1}}\"\nwire_api = \"responses\"\nrequires_openai_auth = true" },
        { path: "~/.codex/auth.json", content: "{\n  \"OPENAI_API_KEY\": \"{{api_key}}\"\n}" },
      ] },
    ],
  },
  codexWs: {
    id: "custom-codex-ws", label: "Codex WebSocket", kind: "codex", enabled: true, sort_order: 120, variants: [
      { id: "unix", label: "macOS / Linux", files: [
        { path: "~/.codex/config.toml", content: "model_provider = \"Sub2API\"\nmodel = \"{{codex_model}}\"\n\n[model_providers.Sub2API]\nbase_url = \"{{base_url_v1}}\"\nwire_api = \"responses\"\nsupports_websockets = true\nrequires_openai_auth = true" },
        { path: "~/.codex/auth.json", content: "{\n  \"OPENAI_API_KEY\": \"{{api_key}}\"\n}" },
      ] },
    ],
  },
  curl: {
    id: "custom-openai-curl", label: "OpenAI cURL", kind: "generic", enabled: true, sort_order: 130, variants: [
      { id: "default", label: "cURL", files: [{ path: "Terminal", content: "curl {{base_url_v1}}/chat/completions \\\n  -H 'Authorization: Bearer {{api_key}}' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"{{codex_model}}\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}'" }] },
    ],
  },
  python: {
    id: "custom-openai-python", label: "Python OpenAI SDK", kind: "generic", enabled: true, sort_order: 140, variants: [
      { id: "default", label: "Python", files: [{ path: "example.py", content: "from openai import OpenAI\n\nclient = OpenAI(api_key=\"{{api_key}}\", base_url=\"{{base_url_v1}}\")\nresponse = client.chat.completions.create(model=\"{{codex_model}}\", messages=[{\"role\": \"user\", \"content\": \"Hello\"}])\nprint(response.choices[0].message.content)" }] },
    ],
  },
};

const presetButtons = [
  { id: "claude", label: "Claude Code" },
  { id: "codex", label: "Codex HTTP" },
  { id: "codexWs", label: "Codex WS" },
  { id: "curl", label: "OpenAI cURL" },
  { id: "python", label: "Python SDK" },
];

function addPreset(index: number, presetId: string) {
  const preset = structuredClone(presets[presetId]);
  const templates = [...props.modelValue[index].templates];
  const existing = templates.findIndex((template) => template.id === preset.id);
  if (existing >= 0) templates.splice(existing, 1, preset);
  else templates.push(preset);
  updateProfile(index, { templates });
}
</script>
