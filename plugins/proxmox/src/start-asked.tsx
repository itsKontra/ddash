import { Button, Dialog, FormActions, Presence } from "@ddash/ui";
import { useEffect } from "react";
import { useSearchParams } from "react-router";

/**
 * ?start on a Workload's detail, as a down App's Concern on the Overview
 * links it: once what the Workload allows is known, asks to start it when
 * it can be (start: stopped, not Protected), since the click was made
 * elsewhere, and runs the Page's own Start on yes. ?start goes once
 * answered, or at once when there is nothing to ask. A link alone never
 * starts anything.
 */
export function StartAsked({ label, start, known }: { label: string; start?: () => void; known: boolean }) {
  const [params, setParams] = useSearchParams();
  const asked = params.has("start") && known;
  const drop = () =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("start");
        return next;
      },
      { replace: true },
    );
  const nothing = asked && !start;
  useEffect(() => {
    if (nothing) drop();
  });

  return (
    <Presence>
      {asked && start && (
        <Dialog
          title={`Start ${label}`}
          subtitle="Proxmox starts it, and ddash ends its Maintenance and probes its Apps' Checks at once."
          onClose={drop}
          footer={
            <FormActions>
              <Button variant="ghost" onClick={drop}>
                Cancel
              </Button>
              <Button
                icon="play"
                onClick={() => {
                  drop();
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
