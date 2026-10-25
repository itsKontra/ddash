import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import { useAction, useQuery } from "@ddash/plugin";
import {
  ConfigErrorsSchema,
  CoreService,
  FormField_Kind,
  type ComponentStatus,
  type FormField,
  type GetConfigFormResponse,
  type TestAction,
} from "@ddash/proto/ddash/core/core_pb";
import type { SetupHint as SetupHintMessage } from "@ddash/proto/ddash/config/config_pb";
import {
  Button,
  CodeBlock,
  Dialog,
  Field,
  FormActions,
  Icon,
  Notice,
  Pill,
  Select,
  Skeleton,
  StatusDot,
  Switch,
  TextInput,
  type Tone,
} from "@ddash/ui";
import { useEffect, useId, useState, type ReactNode } from "react";
import { validate } from "./config-rules";

/** Errors by path, e.g. "hosts[1].url". */
type Errors = Record<string, string>;

/** A secret's edit: kept as stored, replaced by a new value, or cleared. */
type Secret = { mode: "keep" } | { mode: "replace"; value: string } | { mode: "clear" };

/**
 * A Component's config form (or the core's, owner "core"), generated from
 * its config message. It saves through SaveConfig, which restarts the
 * Component, and shows the health polled right after its Start. The form
 * has no submit of its own: a button outside it (a Dialog's footer) names
 * it by formId. onSaving hears whether a save is under way.
 */
export function ConfigForm({ owner, formId, onSaving }: { owner: string; formId: string; onSaving?: (pending: boolean) => void }) {
  const { data, error } = useQuery(CoreService.method.getConfigForm, { owner });
  const [status, setStatus] = useState<ComponentStatus>();
  const [saves, setSaves] = useState(0);
  if (error && !data)
    return (
      <Notice tone="down" title={`${owner}'s config can't be read`}>
        {error.rawMessage}
      </Notice>
    );
  if (!data) return <Skeleton lines={4} />;
  return (
    <div>
      {data.setupHint && <SetupHint hint={data.setupHint} />}
      {/* Only a save starts over from what is stored (its Action has refetched the form by then); other refetches keep the Operator's edits. */}
      <Editor
        key={saves}
        owner={owner}
        form={data}
        formId={formId}
        onSaving={onSaving}
        onSaved={(s) => {
          setStatus(s);
          setSaves((n) => n + 1);
        }}
      />
      {status && <Health status={status} />}
      {data.testAction && <TestButton owner={owner} test={data.testAction} />}
    </div>
  );
}

/** What the Operator does outside ddash to finish the setup, with a Copy button for its block. */
function SetupHint({ hint }: { hint: SetupHintMessage }) {
  return (
    <div className="mb-6 flex flex-col gap-3">
      <Notice tone="info" title="Setup">
        {hint.text}
      </Notice>
      {hint.block && (
        <CodeBlock label="Setup" copy>
          {hint.block}
        </CodeBlock>
      )}
    </div>
  );
}

/** Runs the Component's test_action on its saved config, with the outcome beside it. */
function TestButton({ owner, test }: { owner: string; test: TestAction }) {
  const run = useAction(CoreService.method.runTestAction);
  const [done, setDone] = useState(false);
  const outcome = run.error ? (
    <>
      <StatusDot tone="down" />
      {test.label} failed: {run.error.rawMessage}
    </>
  ) : done ? (
    <>
      <StatusDot tone="up" />
      {test.label}: done
    </>
  ) : null;
  return (
    <div className="mt-6 flex flex-wrap items-center gap-3 rounded-ui bg-muted p-4">
      <Button
        variant="outline"
        disabled={run.pending}
        onClick={async () => {
          setDone(false);
          setDone((await run.run({ owner })) !== undefined);
        }}
      >
        {test.label}
      </Button>
      <output aria-label={`${test.label} result`} className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
        {outcome ?? "Runs on the saved config."}
      </output>
    </div>
  );
}

function Health({ status }: { status: ComponentStatus }) {
  return (
    <p role="status" className="mt-5 flex items-center gap-2 text-sm">
      <Pill tone={toneOf(status.state)} dot>
        {status.state}
      </Pill>
      {status.message && <span className="text-muted-foreground">{status.message}</span>}
    </p>
  );
}

const tones: Record<string, Tone> = { ok: "up", degraded: "warn", unconfigured: "warn", down: "down", crashed: "down" };
const toneOf = (state: string): Tone => tones[state] ?? "idle";

function Editor({
  owner,
  form,
  formId,
  onSaving,
  onSaved,
}: {
  owner: string;
  form: GetConfigFormResponse;
  formId: string;
  onSaving?: (pending: boolean) => void;
  onSaved: (s: ComponentStatus | undefined) => void;
}) {
  const save = useAction(CoreService.method.saveConfig);
  const [values, setValues] = useState<JsonObject>(() => structuredClone(form.values ?? {}));
  const [secrets, setSecrets] = useState<Record<string, Secret>>({});
  const [checked, setChecked] = useState<Errors>({});
  const stored = Object.fromEntries(form.errors.map((e) => [e.path, e.message]));
  const startErrors = Object.fromEntries((save.error?.findDetails(ConfigErrorsSchema)[0]?.errors ?? []).map((e) => [e.path, e.message]));
  const errors: Errors = { ...stored, ...startErrors, ...checked };

  useEffect(() => onSaving?.(save.pending), [onSaving, save.pending]);

  const set = (name: string, value: JsonValue | undefined) =>
    setValues((v) => {
      const next = { ...v };
      if (value === undefined || value === "" || (Array.isArray(value) && value.length === 0)) delete next[name];
      else next[name] = value;
      return next;
    });

  const secretsSet = form.secretsSet.filter((name) => (secrets[name]?.mode ?? "keep") === "keep");

  const submit = async () => {
    if (save.pending) return;
    const out: JsonObject = { ...values };
    const clearSecrets: string[] = [];
    for (const [name, s] of Object.entries(secrets)) {
      if (s.mode === "replace" && s.value !== "") out[name] = s.value;
      if (s.mode === "clear") clearSecrets.push(name);
    }
    const found = validate(form.fields, out, secretsSet);
    setChecked(found);
    if (Object.keys(found).length > 0) return;
    const res = await save.run({ owner, values: out, clearSecrets });
    if (res) onSaved(res.status);
  };

  return (
    <form
      id={formId}
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
      className="flex flex-col gap-5"
    >
      {form.fields.map((f) => (
        <ConfigField
          key={f.name}
          field={f}
          value={values[f.name]}
          onChange={(v) => set(f.name, v)}
          errors={errors}
          secret={f.secret ? { set: form.secretsSet.includes(f.name), edit: secrets[f.name] ?? { mode: "keep" } } : undefined}
          onSecret={(s) => setSecrets((all) => ({ ...all, [f.name]: s }))}
        />
      ))}
      {save.error && !save.error.findDetails(ConfigErrorsSchema).length && (
        <Notice tone="down" title="Save failed">
          {save.error.rawMessage}
        </Notice>
      )}
    </form>
  );
}

interface FieldProps {
  field: FormField;
  value: JsonValue | undefined;
  onChange: (v: JsonValue | undefined) => void;
  errors: Errors;
  secret?: { set: boolean; edit: Secret };
  onSecret: (s: Secret) => void;
}

/** One field of the form: a scalar in a Field, or a group for a secret, a list or a table. */
function ConfigField({ field: f, value, onChange, errors, secret, onSecret }: FieldProps) {
  if (secret) return <SecretField field={f} secret={secret} onSecret={onSecret} error={errors[f.name]} />;
  if (f.repeated)
    return (
      <Group field={f} error={errors[f.name]}>
        {f.kind === FormField_Kind.TABLE ? (
          <Table field={f} rows={Array.isArray(value) ? (value as JsonObject[]) : []} onChange={onChange} errors={errors} />
        ) : (
          <List field={f} items={Array.isArray(value) ? value : []} onChange={onChange} errors={errors} />
        )}
      </Group>
    );
  const hint =
    f.help || f.choicesNote ? (
      <>
        {f.help}
        {f.help && f.choicesNote && " "}
        {f.choicesNote && <span className="text-warn-text">{f.choicesNote}</span>}
      </>
    ) : undefined;
  return (
    <div data-field={f.name} className="relative">
      <Field label={f.label} hint={hint} error={errors[f.name]}>
        <Scalar field={f} value={value} onChange={onChange} />
      </Field>
      <div className="absolute top-0 right-0 flex items-center gap-2">
        {f.required && <Required />}
        {value !== undefined && (
          <button
            type="button"
            aria-label={`Reset ${f.label}`}
            className="cursor-pointer text-xs font-semibold text-primary hover:underline"
            onClick={() => onChange(undefined)}
          >
            Reset
          </button>
        )}
      </div>
    </div>
  );
}

function Required() {
  return <span className="text-[10px] font-bold tracking-wide text-muted-foreground uppercase">required</span>;
}

/** A field of several controls (a list, a table, a secret): a fieldset whose legend looks like a Field's label. */
function Group({ field: f, error, children }: { field: FormField; error?: string; children: ReactNode }) {
  return (
    <fieldset data-field={f.name} className="flex min-w-0 flex-col gap-1.5">
      <legend className="mb-1.5 flex w-full items-center gap-2 text-xs font-semibold text-foreground">
        {f.label}
        {f.required && (
          <span className="ml-auto">
            <Required />
          </span>
        )}
      </legend>
      {children}
      {f.help && <p className="text-xs text-muted-foreground">{f.help}</p>}
      <FieldError message={error} />
    </fieldset>
  );
}

function FieldError({ message }: { message?: string }) {
  return message ? <p className="mt-1 text-xs font-medium text-down-text">{message}</p> : null;
}

/**
 * One string, int, bool or enum value; unset shows the default. label names
 * it when it sits in a list or a table, outside a Field.
 */
function Scalar({ field: f, value, onChange, label }: { field: FormField; value: JsonValue | undefined; onChange: (v: JsonValue | undefined) => void; label?: string }) {
  switch (f.kind) {
    case FormField_Kind.BOOL: {
      const on = value === undefined ? f.default === "true" : value === true;
      return <Switch label={label ?? f.label} checked={on} onChange={onChange} />;
    }
    case FormField_Kind.ENUM:
      return (
        <Select aria-label={label} value={typeof value === "string" ? value : ""} onChange={(e) => onChange(e.target.value || undefined)}>
          <option value="">{f.default ? `Default (${f.options.find((o) => o.value === f.default)?.label ?? f.default})` : "—"}</option>
          {f.options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </Select>
      );
    default:
      if (f.choices.length > 0) return <Choices field={f} value={value} onChange={onChange} label={label} />;
      return (
        <TextInput
          aria-label={label}
          inputMode={f.kind === FormField_Kind.INT ? "numeric" : undefined}
          placeholder={f.placeholder}
          value={value === undefined || value === null ? "" : String(value)}
          onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
        />
      );
  }
}

/**
 * A string picked from the choices its choices_from getter offers. A
 * stored value no longer on offer stays selectable, so opening the form
 * never changes it.
 */
function Choices({ field: f, value, onChange, label }: { field: FormField; value: JsonValue | undefined; onChange: (v: JsonValue | undefined) => void; label?: string }) {
  const current = typeof value === "string" ? value : "";
  const offered = f.choices.some((c) => c.value === current);
  return (
    <Select aria-label={label} value={current} onChange={(e) => onChange(e.target.value || undefined)}>
      <option value="">{f.placeholder || "—"}</option>
      {current && !offered && <option value={current}>{`${current} (not on offer)`}</option>}
      {f.choices.map((c) => (
        <option key={c.value} value={c.value}>
          {c.label}
        </option>
      ))}
    </Select>
  );
}

/** A secret: only set or unset once saved, with Replace and Clear. */
function SecretField({ field: f, secret, onSecret, error }: { field: FormField; secret: { set: boolean; edit: Secret }; onSecret: (s: Secret) => void; error?: string }) {
  const { set, edit } = secret;
  const typing = edit.mode === "replace" || (!set && edit.mode === "keep");
  let control: ReactNode;
  if (typing) {
    control = (
      <div className="flex gap-2">
        <TextInput
          type="password"
          aria-label={f.label}
          autoComplete="off"
          placeholder={f.placeholder}
          value={edit.mode === "replace" ? edit.value : ""}
          onChange={(e) => onSecret({ mode: "replace", value: e.target.value })}
        />
        {set && (
          <Button variant="ghost" onClick={() => onSecret({ mode: "keep" })}>
            Keep
          </Button>
        )}
      </div>
    );
  } else {
    control = (
      <div className="flex items-center gap-2 text-sm">
        <span className="text-muted-foreground">{edit.mode === "clear" ? "cleared on save" : set ? "set" : "unset"}</span>
        {edit.mode === "clear" ? (
          <Button size="sm" variant="ghost" onClick={() => onSecret({ mode: "keep" })}>
            Undo
          </Button>
        ) : (
          <>
            <Button size="sm" variant="outline" onClick={() => onSecret({ mode: "replace", value: "" })}>
              Replace
            </Button>
            <Button size="sm" variant="outline" onClick={() => onSecret({ mode: "clear" })}>
              Clear
            </Button>
          </>
        )}
      </div>
    );
  }
  return (
    <Group field={f} error={error}>
      {control}
    </Group>
  );
}

/** A repeated scalar: one input per item, with Add and Remove. */
function List({ field: f, items, onChange, errors }: { field: FormField; items: JsonValue[]; onChange: (v: JsonValue[]) => void; errors: Errors }) {
  return (
    <div className="flex flex-col gap-2">
      {items.map((item, i) => (
        <div key={i} data-field={`${f.name}[${i}]`}>
          <div className="flex gap-2">
            <Scalar
              field={f}
              label={`${f.label}, item ${i + 1}`}
              value={item}
              onChange={(v) => onChange(items.map((x, j) => (j === i ? (v ?? "") : x)))}
            />
            <Button variant="ghost" aria-label={`Remove item ${i + 1}`} onClick={() => onChange(items.filter((_, j) => j !== i))}>
              <Icon name="x" />
            </Button>
          </div>
          <FieldError message={errors[`${f.name}[${i}]`]} />
        </div>
      ))}
      <div>
        <Button size="sm" variant="outline" icon="plus" aria-label={`Add an item to ${f.label}`} onClick={() => onChange([...items, ""])}>
          Add
        </Button>
      </div>
    </div>
  );
}

/** A repeated message: a row each, a column per field. */
function Table({ field: f, rows, onChange, errors }: { field: FormField; rows: JsonObject[]; onChange: (v: JsonObject[]) => void; errors: Errors }) {
  const setCell = (i: number, name: string, v: JsonValue | undefined) =>
    onChange(
      rows.map((row, j) => {
        if (j !== i) return row;
        const next = { ...row };
        if (v === undefined) delete next[name];
        else next[name] = v;
        return next;
      }),
    );
  return (
    <div className="flex flex-col gap-2">
      <div className="overflow-x-auto">
        <table aria-label={f.label} className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-muted-foreground">
              {f.columns.map((c) => (
                <th key={c.name} className="pb-1 font-semibold">
                  {c.label}
                </th>
              ))}
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <tr key={i} className="align-top">
                {f.columns.map((c) => {
                  const path = `${f.name}[${i}].${c.name}`;
                  return (
                    <td key={c.name} data-field={path} className="min-w-32 pr-2 pb-2">
                      <Scalar field={c} label={`${c.label}, row ${i + 1}`} value={row[c.name]} onChange={(v) => setCell(i, c.name, v)} />
                      <FieldError message={errors[path]} />
                    </td>
                  );
                })}
                <td className="pb-2">
                  <Button variant="ghost" aria-label={`Remove row ${i + 1}`} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
                    <Icon name="x" />
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div>
        <Button size="sm" variant="outline" icon="plus" aria-label={`Add a row to ${f.label}`} onClick={() => onChange([...rows, {}])}>
          Add row
        </Button>
      </div>
    </div>
  );
}

/**
 * The config form in a centred Dialog, titled by the Component, with
 * Cancel and Save and restart pinned in its footer.
 */
export function ConfigDialog({ owner, onClose }: { owner: string; onClose: () => void }) {
  const formId = useId();
  const [saving, setSaving] = useState(false);
  return (
    <Dialog
      kicker="Configure"
      title={owner}
      subtitle={`Saving restarts ${owner} with the new config. Unset fields take their defaults.`}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={formId} disabled={saving}>
            Save and restart
          </Button>
        </FormActions>
      }
    >
      <ConfigForm owner={owner} formId={formId} onSaving={setSaving} />
    </Dialog>
  );
}
