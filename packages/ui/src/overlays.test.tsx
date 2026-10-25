import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import { Button, ConfirmDialog, Dialog, Menu, Sheet } from "./index";

afterEach(cleanup);

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
