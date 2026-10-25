import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Button, CodeBlock, EmptyState, ErrorFrame, Notice } from "./index";

afterEach(cleanup);

it("Notice down is an alert, tinted down, with its title over its text", () => {
  render(
    <Notice tone="down" title="Proxmox unreachable">
      connect: connection refused
    </Notice>,
  );

  const notice = screen.getByRole("alert");
  expect(notice.textContent).toContain("Proxmox unreachable");
  expect(notice.textContent).toContain("connect: connection refused");
  expect(notice.className.split(" ")).toEqual(expect.arrayContaining(["bg-down-tint", "text-down-text"]));
});

it("Notice warn and info are polite status messages in their own tint", () => {
  render(
    <>
      <Notice tone="warn">2 of 3 hosts answered</Notice>
      <Notice tone="info">A newer Release is available</Notice>
    </>,
  );

  const [warn, info] = screen.getAllByRole("status");
  expect(warn.textContent).toBe("2 of 3 hosts answered");
  expect(warn.className).toContain("bg-warn-tint");
  expect(info.textContent).toBe("A newer Release is available");
  expect(info.className).toContain("bg-info-tint");
  expect(screen.queryByRole("alert")).toBeNull();
});

it("EmptyState says what is missing and offers what to do about it", () => {
  let configured = false;
  render(
    <EmptyState
      icon="inbox"
      title="Docker isn't configured"
      action={
        <Button
          onClick={() => {
            configured = true;
          }}
        >
          Configure
        </Button>
      }
    >
      Add a host to see its containers.
    </EmptyState>,
  );

  expect(screen.getByText("Docker isn't configured")).toBeTruthy();
  expect(screen.getByText("Add a host to see its containers.")).toBeTruthy();
  expect(document.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Configure" }));
  expect(configured).toBe(true);
});

it("EmptyState needs only a title", () => {
  const { container } = render(<EmptyState title="No Checks yet" />);

  expect(container.textContent).toBe("No Checks yet");
  expect(container.querySelector("svg")).toBeNull();
});

it("CodeBlock is a labelled region keyboard users can scroll sideways, its lines never wrapped", () => {
  render(<CodeBlock label="Caddyfile">{"nas.lan {\n  reverse_proxy 10.0.0.5:5000\n}"}</CodeBlock>);

  const block = screen.getByRole("region", { name: "Caddyfile" });
  expect(block.tagName).toBe("PRE");
  expect(block.textContent).toBe("nas.lan {\n  reverse_proxy 10.0.0.5:5000\n}");
  expect(block.tabIndex).toBe(0);
  expect(block.className.split(" ")).toEqual(expect.arrayContaining(["overflow-x-auto", "whitespace-pre"]));
});

it("CodeBlock without a label is plain preformatted text", () => {
  render(<CodeBlock>docker ps</CodeBlock>);

  expect(screen.queryByRole("region")).toBeNull();
  expect(screen.getByText("docker ps").tagName).toBe("PRE");
});

it("ErrorFrame alerts with its title and shows the error's message as code", () => {
  render(<ErrorFrame title="Checks crashed" error={new Error("TypeError: rows is undefined")} />);

  const frame = screen.getByRole("alert");
  expect(screen.getByText("Checks crashed")).toBeTruthy();
  const message = screen.getByText("TypeError: rows is undefined");
  expect(message.tagName).toBe("PRE");
  expect(frame.contains(message)).toBe(true);
});
