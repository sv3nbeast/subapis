import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AdminGroup, CompositeModelRoute } from "@/types";
import CompositeRoutesModal from "../CompositeRoutesModal.vue";

const {
  listCompositeRoutes,
  createCompositeRoute,
  updateCompositeRoute,
  deleteCompositeRoute,
  previewCompositeRoute,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  listCompositeRoutes: vi.fn(),
  createCompositeRoute: vi.fn(),
  updateCompositeRoute: vi.fn(),
  deleteCompositeRoute: vi.fn(),
  previewCompositeRoute: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock("@/api/admin", () => ({
  adminAPI: {
    groups: {
      listCompositeRoutes,
      createCompositeRoute,
      updateCompositeRoute,
      deleteCompositeRoute,
      previewCompositeRoute,
    },
  },
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({ showError, showSuccess }),
}));

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string>) =>
        params?.name ? `${key}:${params.name}` : key,
    }),
  };
});

const group = {
  id: 99,
  name: "Unified Route",
  platform: "composite",
} as AdminGroup;

const savedRoute: CompositeModelRoute = {
  id: 7,
  group_id: group.id,
  public_model: "public-model",
  match_type: "exact",
  target_platform: "openai",
  upstream_model: "gpt-5",
  endpoint: "responses",
  priority: 20,
  enabled: true,
  notes: "",
};

const BaseDialogStub = {
  props: ["show", "title"],
  emits: ["close"],
  template:
    '<div v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
};

const SelectStub = {
  props: ["modelValue", "options"],
  emits: ["update:modelValue"],
  template: `
    <select :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
      <option v-for="option in options" :key="option.value" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `,
};

function mountModal() {
  return mount(CompositeRoutesModal, {
    props: { show: true, group },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        PlatformIcon: true,
        Icon: true,
      },
    },
  });
}

describe("CompositeRoutesModal", () => {
  beforeEach(() => {
    for (const fn of [
      listCompositeRoutes,
      createCompositeRoute,
      updateCompositeRoute,
      deleteCompositeRoute,
      previewCompositeRoute,
      showError,
      showSuccess,
    ]) {
      fn.mockReset();
    }
    listCompositeRoutes.mockResolvedValue([savedRoute]);
    createCompositeRoute.mockResolvedValue(savedRoute);
    updateCompositeRoute.mockResolvedValue(savedRoute);
    deleteCompositeRoute.mockResolvedValue(undefined);
    previewCompositeRoute.mockResolvedValue({
      matched: true,
      source: "route",
      group_id: group.id,
      public_model: "public-model",
      target_platform: "openai",
      upstream_model: "gpt-5",
      endpoint: "responses",
    });
  });

  it("loads routes and creates a new route for the selected composite group", async () => {
    const wrapper = mountModal();
    await flushPromises();

    expect(listCompositeRoutes).toHaveBeenCalledWith(group.id);
    await wrapper
      .get('[data-testid="composite-public-model"]')
      .setValue("new-public-model");
    await wrapper.get('[data-testid="composite-route-form"]').trigger("submit");
    await flushPromises();

    expect(createCompositeRoute).toHaveBeenCalledWith(
      group.id,
      expect.objectContaining({
        public_model: "new-public-model",
        match_type: "exact",
        target_platform: "openai",
        endpoint: "any",
      }),
    );
    expect(showSuccess).toHaveBeenCalledWith(
      "admin.groups.compositeRoutes.routeCreated",
    );
    expect(listCompositeRoutes).toHaveBeenCalledTimes(2);
  });

  it.each(['kimi', 'zhipu', 'deepseek'])('saves a %s composite route from the rendered picker', async (platform) => {
    const wrapper = mountModal();
    await flushPromises();
    const picker = wrapper.findAll('select').find(s =>
      s.findAll('option').some(o => o.attributes('value') === 'zhipu')
    );
    expect(picker).toBeDefined();
    await picker!.setValue(platform);
    await wrapper.get('[data-testid="composite-public-model"]').setValue('public-cn');
    await wrapper.get('[data-testid="composite-route-form"]').trigger('submit');
    await flushPromises();
    expect(createCompositeRoute).toHaveBeenCalledWith(group.id, expect.objectContaining({
      target_platform: platform, public_model: 'public-cn'
    }));
    wrapper.unmount();
  });

  it("updates and deletes an existing route", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const wrapper = mountModal();
    await flushPromises();

    await wrapper
      .get('[data-testid="composite-route-edit-7"]')
      .trigger("click");
    await wrapper
      .get('[data-testid="composite-public-model"]')
      .setValue("renamed-model");
    await wrapper.get('[data-testid="composite-route-form"]').trigger("submit");
    await flushPromises();

    expect(updateCompositeRoute).toHaveBeenCalledWith(
      group.id,
      savedRoute.id,
      expect.objectContaining({ public_model: "renamed-model" }),
    );

    await wrapper
      .get('[data-testid="composite-route-delete-7"]')
      .trigger("click");
    await flushPromises();
    expect(deleteCompositeRoute).toHaveBeenCalledWith(group.id, savedRoute.id);
    confirm.mockRestore();
  });

  it("previews the route decision through the backend contract", async () => {
    const wrapper = mountModal();
    await flushPromises();

    await wrapper
      .get('[data-testid="composite-preview-model"]')
      .setValue("public-model");
    await wrapper
      .get('[data-testid="composite-preview-submit"]')
      .trigger("click");
    await flushPromises();

    expect(previewCompositeRoute).toHaveBeenCalledWith(group.id, {
      model: "public-model",
      endpoint: "any",
    });
    expect(
      wrapper.get('[data-testid="composite-preview-result"]').text(),
    ).toContain("admin.groups.compositeRoutes.matched");
  });

  it("ignores a preview response from a previously opened group", async () => {
    let resolvePreview!: (value: Record<string, unknown>) => void;
    previewCompositeRoute.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePreview = resolve;
        }),
    );
    const wrapper = mountModal();
    await flushPromises();

    await wrapper
      .get('[data-testid="composite-preview-model"]')
      .setValue("public-model");
    void wrapper.get('[data-testid="composite-preview-submit"]').trigger("click");
    await wrapper.vm.$nextTick();

    await wrapper.setProps({ show: false, group: null });
    await wrapper.setProps({
      show: true,
      group: { ...group, id: 100, name: "Other Group" },
    });
    resolvePreview({
      matched: true,
      source: "route",
      group_id: group.id,
      public_model: "public-model",
      target_platform: "openai",
      upstream_model: "gpt-5",
      endpoint: "responses",
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="composite-preview-result"]').exists()).toBe(
      false,
    );
  });
});
