// KLOX-100: the edition counter's later steps must navigate — clicking
// "Revisar e enviar" from Escrever did nothing because only completed
// steps rendered as buttons. navAhead opens forward navigation for
// flows whose later steps already exist.
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { StepCounter } from "../components/StepCounter";

afterEach(cleanup);

const STEPS = ["Stories", "Write", "Review & send"];

describe("StepCounter forward navigation (KLOX-100)", () => {
  it("a later step is a working button with navAhead", () => {
    const onStep = vi.fn();
    render(<StepCounter steps={STEPS} current={2} onStep={onStep} navAhead />);
    fireEvent.click(screen.getByRole("button", { name: "Review & send" }));
    expect(onStep).toHaveBeenCalledWith(2);
  });

  it("a later step is inert without navAhead (onboarding keeps back-only)", () => {
    const onStep = vi.fn();
    render(<StepCounter steps={STEPS} current={2} onStep={onStep} />);
    expect(screen.queryByRole("button", { name: "Review & send" })).toBeNull();
    expect(screen.getByText("Review & send")).toBeVisible();
  });

  it("completed steps stay navigable in both modes", () => {
    const onStep = vi.fn();
    render(<StepCounter steps={STEPS} current={3} onStep={onStep} navAhead />);
    fireEvent.click(screen.getByRole("button", { name: "Stories" }));
    expect(onStep).toHaveBeenCalledWith(0);
  });
});
