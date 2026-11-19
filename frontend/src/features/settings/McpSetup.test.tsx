import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock(
  "../../../bindings/lontar/internal/services",
  () => import("../../test/mockBindings"),
);
const copyText = vi.fn<(text: string) => Promise<void>>();
vi.mock("../../lib/clipboard", () => ({
  copyText: (t: string) => copyText(t),
}));

import { AppService, resetBindings } from "../../test/mockBindings";
import McpSetupSection from "./McpSetup";

describe("McpSetupSection", () => {
  beforeEach(() => {
    resetBindings();
    copyText.mockReset();
  });
  afterEach(cleanup);

  it("shows and copies the commands for this installation", async () => {
    copyText
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error("denied"));
    const user = userEvent.setup();
    render(<McpSetupSection />);
    expect(
      await screen.findByLabelText("Claude Code command"),
    ).toHaveTextContent("claude mcp add lontar -- /usr/bin/lontar mcp");
    expect(
      screen.getByLabelText("Claude Desktop configuration"),
    ).toHaveTextContent('"command":"/usr/bin/lontar"');

    await user.click(
      screen.getByRole("button", { name: "Copy Claude Code command" }),
    );
    expect(copyText).toHaveBeenCalledWith(
      "claude mcp add lontar -- /usr/bin/lontar mcp",
    );
    expect(await screen.findByText("Copied")).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Copy Claude Desktop configuration" }),
    );
    expect(await screen.findByText("Copy failed: denied")).toBeInTheDocument();
  });

  it("shows why the executable could not be found", async () => {
    AppService.MCPSetup.mockResolvedValue({
      command: "",
      args: null,
      claudeCode: "",
      claudeDesktop: "",
      error: "could not locate the Lontar executable",
    });
    render(<McpSetupSection />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "could not locate",
    );
    expect(screen.queryByLabelText("Claude Code command")).toBeNull();
  });

  it("shows a failed call", async () => {
    AppService.MCPSetup.mockRejectedValue(new Error("boom"));
    render(<McpSetupSection />);
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
  });

  it("ignores a result after unmount", async () => {
    let resolve: (v: unknown) => void = () => {};
    AppService.MCPSetup.mockReturnValue(new Promise((r) => (resolve = r)));
    const { unmount } = render(<McpSetupSection />);
    unmount();
    resolve({ claudeCode: "x", claudeDesktop: "y", error: "" });
    await waitFor(() => expect(AppService.MCPSetup).toHaveBeenCalled());
  });
});
