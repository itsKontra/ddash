import { Button, Dialog, FormActions, Presence } from "@ddash/ui";
import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";

/**
 * ?start on a Workload's detail, as a down App's Concern on the Overview
 * links it: once what the Workload allows is known, drops ?start and,
 * when it can be started (start: stopped, not Protected), asks first,
 * since the click was made elsewhere, then runs the Page's own Start. A
 * link alone never starts anything.
 */
export function StartAsked({ label, start, known }: { label: string; start?: () => void; known: boolean }) {
  const [params, setParams] = useSearchParams();
  const asked = params.has("start");
  const [open, setOpen] = useState(false);
  // Asked once per ?start, though the effect runs again before it's dropped.
  const done = useRef(false);
  useEffect(() => {
    if (!asked) done.current = false;
    if (!asked || !known || done.current) return;
    done.current = true;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("start");
        return next;
      },
      { replace: true },
    );
    if (start) setOpen(true);
  }, [asked, known, start, setParams]);

  const close = () => setOpen(false);
  return (
    <Presence>
      {open && start && (
        <Dialog
          title={`Start ${label}`}
          subtitle="Proxmox starts it, and ddash ends its Maintenance and probes its Apps' Checks at once."
          onClose={close}
          footer={
            <FormActions>
              <Button variant="ghost" onClick={close}>
                Cancel
              </Button>
              <Button
                icon="play"
                onClick={() => {
                  close();
                  start();
                }}
              >
                Start
              </Button>
            </FormActions>
          }
        />
      )}
    </Presence>
  );
}
