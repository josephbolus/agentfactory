import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { api } from "./api";

describe("App brand", () => {
  it("shows the Agent Factory anvil mark", async () => {
    vi.spyOn(api, "workers").mockResolvedValue([]);
    vi.spyOn(api, "runs").mockResolvedValue({ runs: [], next_cursor: null });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    const { container } = render(<QueryClientProvider client={client}><App /></QueryClientProvider>);

    expect(await screen.findByText("Agent Factory", { exact: true })).toBeVisible();
    expect(container.querySelector(".brand-mark .lucide-anvil")).toBeInTheDocument();
  });
});
