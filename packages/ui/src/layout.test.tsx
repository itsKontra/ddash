import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { PageHeader } from "./index";

afterEach(cleanup);

it("PageHeader shows the title as the Page's heading, with subtitle and actions", () => {
  render(<PageHeader title="Docker" subtitle="Containers per host" actions={<button>Refresh</button>} />);

  expect(screen.getByRole("heading", { level: 1, name: "Docker" })).toBeTruthy();
  expect(screen.getByText("Containers per host")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeTruthy();
});
