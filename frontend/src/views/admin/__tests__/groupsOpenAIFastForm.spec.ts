import { describe, expect, it } from "vitest";

import { groupOpenAIFastFormState } from "../groupsOpenAIFastForm";

describe("groupOpenAIFastFormState", () => {
  it("hydrates a group with forced fast enabled", () => {
    expect(
      groupOpenAIFastFormState({
        platform: "openai",
        force_openai_fast: true,
        free_openai_fast: false,
      }),
    ).toEqual({ force_openai_fast: true, free_openai_fast: false });
  });

  it("hydrates free fast independently of forced fast", () => {
    expect(
      groupOpenAIFastFormState({
        platform: "openai",
        force_openai_fast: false,
        free_openai_fast: true,
      }),
    ).toEqual({ force_openai_fast: false, free_openai_fast: true });

    expect(
      groupOpenAIFastFormState({
        platform: "openai",
        force_openai_fast: true,
        free_openai_fast: true,
      }),
    ).toEqual({ force_openai_fast: true, free_openai_fast: true });
  });

  it("treats missing flags as disabled", () => {
    expect(groupOpenAIFastFormState({ platform: "openai" })).toEqual({
      force_openai_fast: false,
      free_openai_fast: false,
    });
    expect(groupOpenAIFastFormState(null)).toEqual({
      force_openai_fast: false,
      free_openai_fast: false,
    });
  });

  it("never carries state across groups", () => {
    // Regression: editing group A (forced fast on) then group B must not leak A's
    // value into B's form, which previously caused B to be saved with fast forced.
    const groupA = groupOpenAIFastFormState({ platform: "openai", force_openai_fast: true });
    const groupB = groupOpenAIFastFormState({ platform: "openai", force_openai_fast: false });
    expect(groupA.force_openai_fast).toBe(true);
    expect(groupB.force_openai_fast).toBe(false);
  });
});
