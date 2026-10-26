import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { Button, ConfirmDialog, Dialog, Menu, Presence, Sheet } from "./index";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  delete (Element.prototype as Partial<Element>).getAnimations;
});

it("Menu opens its items from a labelled button and closes once one is picked", () => {
  const picked: string[] = [];
  render(
    <Menu
      label="Actions for media-stack"
      items={[
        { label: "Stop", onSelect: () => picked.push("Stop") },
        { label: "Start", onSelect: () => picked.push("Start"), disabled: true },
      ]}
    />,
  );

  expect(screen.queryByRole("menu")).toBeNull();
  const button = screen.getByRole("button", { name: "Actions for media-stack" });
  expect(button.getAttribute("aria-haspopup")).toBe("menu");
  fireEvent.click(button);
  expect(button.getAttribute("aria-expanded")).toBe("true");
  expect((screen.getByRole("menuitem", { name: "Start" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("menuitem", { name: "Stop" }));

  expect(picked).toEqual(["Stop"]);
  expect(screen.queryByRole("menu")).toBeNull();
});

it("Menu closes on Escape without picking", () => {
  render(<Menu label="Actions" items={[{ label: "Stop", onSelect: () => {} }]} />);

  fireEvent.click(screen.getByRole("button", { name: "Actions" }));
  fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });

  expect(screen.queryByRole("menu")).toBeNull();
});

it("Dialog is a modal dialog named by its title, with the kicker and subtitle around it", () => {
  render(
    <Dialog kicker="Configure" title="docker-api" subtitle="Saving restarts docker-api." onClose={() => {}}>
      <p>Body</p>
    </Dialog>,
  );

  const dialog = screen.getByRole("dialog", { name: "docker-api" });
  expect(dialog.getAttribute("aria-modal")).toBe("true");
  expect(within(dialog).getByRole("heading", { name: "docker-api" })).toBeTruthy();
  expect(within(dialog).getByText("Configure")).toBeTruthy();
  expect(document.getElementById(dialog.getAttribute("aria-describedby") ?? "")?.textContent).toBe("Saving restarts docker-api.");
});

it("Dialog closes on Escape, on the overlay and on Close, but not on a click inside", () => {
  let closed = 0;
  render(
    <Dialog title="Edit" onClose={() => closed++}>
      <p>Body</p>
    </Dialog>,
  );
  const dialog = screen.getByRole("dialog", { name: "Edit" });

  fireEvent.click(screen.getByText("Body"));
  expect(closed).toBe(0);
  fireEvent.keyDown(screen.getByText("Body"), { key: "Escape" });
  expect(closed).toBe(1);
  fireEvent.click(dialog.parentElement!);
  expect(closed).toBe(2);
  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
  expect(closed).toBe(3);
});

it.each(["Dialog", "Sheet"] as const)("%s pins the footer outside the scrolling body, and a submit there sends the body's form", (overlay) => {
  const Overlay = overlay === "Dialog" ? Dialog : Sheet;
  const sent: string[] = [];
  render(
    <Overlay
      title="Add App"
      onClose={() => {}}
      footer={
        <Button type="submit" form="add-app">
          Save
        </Button>
      }
    >
      <form
        id="add-app"
        onSubmit={(e) => {
          e.preventDefault();
          sent.push(new FormData(e.currentTarget).get("name") as string);
        }}
      >
        <input aria-label="Name" name="name" defaultValue="Plex" />
      </form>
    </Overlay>,
  );

  const body = screen.getByRole("textbox", { name: "Name" }).closest("[data-scrolls]");
  const save = screen.getByRole("button", { name: "Save" });
  expect(body).not.toBeNull();
  expect(body!.contains(save)).toBe(false);
  expect(screen.getByRole("dialog", { name: "Add App" }).contains(save)).toBe(true);

  fireEvent.click(save);
  expect(sent).toEqual(["Plex"]);
});

it("ConfirmDialog asks its title with the text under it, focuses Cancel, and answers with the checkbox", () => {
  const answers: unknown[] = [];
  render(
    <ConfirmDialog
      request={{ title: "Uninstall Docker?", text: "Its Widgets leave the Overview.", action: "Uninstall", checkbox: "Also delete data" }}
      onAnswer={(a) => answers.push(a)}
    />,
  );

  const dialog = screen.getByRole("alertdialog", { name: "Uninstall Docker?" });
  expect(within(dialog).getByRole("heading", { name: "Uninstall Docker?" })).toBeTruthy();
  expect(document.getElementById(dialog.getAttribute("aria-describedby") ?? "")?.textContent).toBe("Its Widgets leave the Overview.");
  expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "Cancel" }));

  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Also delete data" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "Uninstall" }));
  expect(answers).toEqual([{ checked: true }]);
});

it("ConfirmDialog without a title asks its text, and Cancel, Escape and the overlay cancel", () => {
  const answers: unknown[] = [];
  render(<ConfirmDialog request={{ text: "Stop container plex on media?" }} onAnswer={(a) => answers.push(a)} />);

  const dialog = screen.getByRole("alertdialog", { name: "Stop container plex on media?" });
  expect(within(dialog).queryByRole("checkbox")).toBeNull();
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  fireEvent.keyDown(within(dialog).getByRole("button", { name: "Confirm" }), { key: "Escape" });
  fireEvent.click(dialog.parentElement!);
  expect(answers).toEqual([null, null, null]);
});

function Opener({ overlay }: { overlay: "Dialog" | "Sheet" }) {
  const [open, setOpen] = useState(false);
  const Overlay = overlay === "Dialog" ? Dialog : Sheet;
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open
      </button>
      {open && (
        <Overlay title="Rename" onClose={() => setOpen(false)} footer={<button type="button">Save</button>}>
          <input aria-label="Name" />
        </Overlay>
      )}
    </>
  );
}

it.each(["Dialog", "Sheet"] as const)("%s traps the focus inside and hands it back to the opener on close", (overlay) => {
  render(<Opener overlay={overlay} />);
  const opener = screen.getByRole("button", { name: "Open" });
  opener.focus();
  fireEvent.click(opener);

  const dialog = screen.getByRole("dialog", { name: "Rename" });
  expect(dialog.contains(document.activeElement)).toBe(true);
  const close = within(dialog).getByRole("button", { name: "Close" });
  const save = within(dialog).getByRole("button", { name: "Save" });

  save.focus();
  fireEvent.keyDown(save, { key: "Tab" });
  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(save);

  fireEvent.keyDown(save, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
});

/**
 * Stubs getAnimations (jsdom has none) with one running animation per call,
 * whose finished the test settles: finish() ends the latest, cancel() aborts it.
 */
function stubAnimations() {
  const calls: { finish: () => void; cancel: () => void }[] = [];
  Element.prototype.getAnimations = function () {
    let finish = () => {};
    let cancel = () => {};
    const finished = new Promise<Animation>((resolve, reject) => {
      finish = () => resolve(this as unknown as Animation);
      cancel = () => reject(new DOMException("aborted", "AbortError"));
    });
    finished.catch(() => {});
    calls.push({ finish, cancel });
    return [{ finished } as Animation];
  };
  return calls;
}

function Toggle({ initial = true }: { initial?: boolean }) {
  const [open, setOpen] = useState(initial);
  const [closes, setCloses] = useState(0);
  return (
    <>
      <button type="button" onClick={() => setOpen(!open)}>
        Toggle
      </button>
      <output aria-label="Closes">{closes}</output>
      <Presence>
        {open && (
          <Sheet
            title="Rename"
            onClose={() => {
              setCloses((n) => n + 1);
              setOpen(false);
            }}
          >
            <input aria-label="Name" />
          </Sheet>
        )}
      </Presence>
    </>
  );
}

it("Presence keeps a closed Sheet mounted, closed and inert, until its exit finishes, with the focus back on the opener", async () => {
  const exits = stubAnimations();
  render(<Toggle initial={false} />);
  const toggle = screen.getByRole("button", { name: "Toggle" });
  toggle.focus();
  fireEvent.click(toggle);
  const dialog = screen.getByRole("dialog", { name: "Rename" });
  expect(dialog.contains(document.activeElement)).toBe(true);

  fireEvent.keyDown(within(dialog).getByRole("textbox"), { key: "Escape" });

  expect(dialog.isConnected).toBe(true);
  const leaving = dialog.closest("[data-state]")!;
  expect(leaving.getAttribute("data-state")).toBe("closed");
  expect(leaving.hasAttribute("inert")).toBe(true);
  expect(document.activeElement).toBe(toggle);

  // A second close during the exit does nothing.
  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
  expect(screen.getByRole("status", { name: "Closes" }).textContent).toBe("1");

  await act(async () => exits.at(-1)!.finish());
  expect(dialog.isConnected).toBe(false);
});

it("Presence cancels the exit when its child returns mid-exit, keeping the same Sheet open", async () => {
  const exits = stubAnimations();
  render(<Toggle />);
  const dialog = screen.getByRole("dialog", { name: "Rename" });
  fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "plex" } });
  const toggle = screen.getByRole("button", { name: "Toggle" });

  fireEvent.click(toggle);
  fireEvent.click(toggle);

  const box = dialog.closest("[data-state]")!;
  expect(box.getAttribute("data-state")).toBe("open");
  expect(box.hasAttribute("inert")).toBe(false);
  // The reversed transition aborts the exit's animation.
  await act(async () => exits.at(-1)!.cancel());
  expect(dialog.isConnected).toBe(true);
  expect((within(dialog).getByRole("textbox") as HTMLInputElement).value).toBe("plex");
});

it("Presence swaps a child that changes while open in place, with no exit", () => {
  const exits = stubAnimations();
  const sheet = (check: string) => (
    <Presence>
      <Sheet title={check} onClose={() => {}}>
        <p>Detail</p>
      </Sheet>
    </Presence>
  );
  const { rerender } = render(sheet("Check A"));
  const dialog = screen.getByRole("dialog", { name: "Check A" });

  rerender(sheet("Check B"));

  expect(screen.getByRole("dialog", { name: "Check B" })).toBe(dialog);
  expect(screen.queryByRole("dialog", { name: "Check A" })).toBeNull();
  expect(exits).toHaveLength(0);
});

it("Presence unmounts at once when nothing animates the exit", () => {
  const { rerender } = render(
    <Presence>
      <Dialog title="Edit" onClose={() => {}} />
    </Presence>,
  );

  rerender(<Presence>{false}</Presence>);

  expect(screen.queryByRole("dialog")).toBeNull();
});
