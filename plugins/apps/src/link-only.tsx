import { useAction } from "@ddash/plugin";
import { AppsService } from "@ddash/proto/apps/apps_pb";
import { Button, FormActions, Panel } from "@ddash/ui";
import { useState, type FormEvent } from "react";
import { Failed, GroupField, IconField, Text } from "./fields";

/**
 * Adds a Link-only App in the pane: a name, a URL, an icon and a group;
 * ddash keeps the link and its Check only. onDone gets the new App's id,
 * or none on Cancel.
 */
export function AddLinkOnly({ groups, onDone }: { groups: readonly string[]; onDone: (id?: string) => void }) {
  const create = useAction(AppsService.method.createApp);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [icon, setIcon] = useState("");
  const [group, setGroup] = useState("");

  const add = async (e: FormEvent) => {
    e.preventDefault();
    const done = await create.run({ app: { name, url, icon, group } });
    if (done) onDone(done.app?.id);
  };

  return (
    <Panel title="Add App" description="A Link-only App: ddash keeps its link and its Check, and changes nothing in pfSense or Nginx Proxy Manager.">
      <form onSubmit={(e) => void add(e)} className="flex flex-col gap-4">
        <Text label="Name" value={name} onChange={setName} />
        <Text label="URL" value={url} onChange={setUrl} placeholder="https://198.18.0.1" />
        <IconField value={icon} onChange={setIcon} name={name} url={url} />
        <GroupField value={group} onChange={setGroup} groups={groups} />
        <Failed error={create.error} />
        <FormActions>
          <Button variant="ghost" onClick={() => onDone()}>
            Cancel
          </Button>
          <Button type="submit" disabled={create.pending}>
            Add
          </Button>
        </FormActions>
      </form>
    </Panel>
  );
}
