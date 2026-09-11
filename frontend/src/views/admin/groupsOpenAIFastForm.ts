/**
 * Form binding helpers for the group-level OpenAI Fast switches.
 *
 * Both switches are admin-only request-policy flags, so they must always be
 * hydrated from the group being edited. Leaving them out of `handleEdit` made
 * the dialog render a stale value and, worse, silently submitted the stale
 * value back to the server when saving an unrelated group.
 */

export interface GroupOpenAIFastSource {
  platform: string;
  force_openai_fast?: boolean;
  free_openai_fast?: boolean;
}

export interface GroupOpenAIFastFormState {
  force_openai_fast: boolean;
  free_openai_fast: boolean;
}

/** Hydrate the edit-form fast switches from the group being edited. */
export function groupOpenAIFastFormState(
  group: GroupOpenAIFastSource | null | undefined,
): GroupOpenAIFastFormState {
  if (!group) {
    return { force_openai_fast: false, free_openai_fast: false };
  }
  return {
    force_openai_fast: group.force_openai_fast ?? false,
    free_openai_fast: group.free_openai_fast ?? false,
  };
}
