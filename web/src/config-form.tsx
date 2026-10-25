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
import { Button, CodeBlock, Dialog, FormActions, Notice, StatePill, StatusDot, Switch, type Tone } from "@ddash/ui";
import { X } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { ErrorFrame } from "./boundary";
import { validate } from "./config-rules";

/** Errors by path, e.g. "hosts[1].url". */
type Errors = Record<string, string>;

/** A secret's edit: kept as stored, replaced by a new value, or cleared. */
type Secret = { mode: "keep" } | { mode: "replace"; value: string } | { mode: "clear" };

/**
 * A Component's config form (or the core's, owner "core"), generated from
 * its config message. It saves through SaveConfig, which restarts the
 * Component, and shows the health polled right after its Start.
 */
export function ConfigForm({ owner, formId }: { owner: string; formId?: string }) {
  const { data, error, dataUpdatedAt } = useQuery(CoreService.method.getConfigForm, { owner });
  const [status, setStatus] = useState<ComponentStatus>();
  if (error && !data) return <ErrorFrame title={`${owner}'s config can't be read`} error={error} />;
  if (!data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <div>
      {data.setupHint && <SetupHint hint={data.setupHint} />}
      {/* A save refetches the form; start over from what is stored then. */}
      <Editor key={dataUpdatedAt} owner={owner} form={data} onSaved={setStatus} formId={formId} />
      {status && <Health status={status} />}
      {/* Outside the Editor: its success refetches the form, which remounts the Editor. */}
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
    <div className="mt-6 flex flex-wrap items-center gap-3 rounded-ui border border-border bg-muted/40 p-4">
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
    <p role="status" className="mt-4 flex items-center gap-2 text-sm">
      <StatePill state={status.state} />
      {status.message && <span className="text-muted-foreground">{status.message}</span>}
    </p>
  );
}

const tones: Record<string, Tone> = { ok: "up", degraded: "warn", unconfigured: "warn", down: "down", crashed: "down" };
const toneOf = (state: string): Tone => tones[state] ?? "idle";

function Editor({ owner, form, onSaved, formId }: { owner: string; form: GetConfigFormResponse; onSaved: (s: ComponentStatus | undefined) => void; formId?: string }) {
  const save = useAction(CoreService.method.saveConfig);
  const [values, setValues] = useState<JsonObject>(() => structuredClone(form.values ?? {}));
  const [secrets, setSecrets] = useState<Record<string, Secret>>({});
  const [checked, setChecked] = useState<Errors>({});
  const stored = Object.fromEntries(form.errors.map((e) => [e.path, e.message]));
  const startErrors = Object.fromEntries((save.error?.findDetails(ConfigErrorsSchema)[0]?.errors ?? []).map((e) => [e.path, e.message]));
  const errors: Errors = { ...stored, ...startErrors, ...checked };

  const set = (name: string, value: JsonValue | undefined) =>
    setValues((v) => {
      const next = { ...v };
      if (value === undefined || value === "" || (Array.isArray(value) && value.length === 0)) delete next[name];
      else next[name] = value;
      return next;
    });

  const secretsSet = form.secretsSet.filter((name) => (secrets[name]?.mode ?? "keep") === "keep");

  const submit = async () => {
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
        <Field
          key={f.name}
          field={f}
          value={values[f.name]}
          onChange={(v) => set(f.name, v)}
          errors={errors}
          secret={f.secret ? { set: form.secretsSet.includes(f.name), edit: secrets[f.name] ?? { mode: "keep" } } : undefined}
          onSecret={(s) => setSecrets((all) => ({ ...all, [f.name]: s }))}
        />
      ))}
      {save.error && !save.error.findDetails(ConfigErrorsSchema).length && <ErrorFrame title="Save failed" error={save.error} />}
      {/* PROTOTYPE: with a formId the container's foot holds Save. */}
      {!formId && (
        <FormActions>
          <Button type="submit" disabled={save.pending}>
            Save
          </Button>
        </FormActions>
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

function Field({ field: f, value, onChange, errors, secret, onSecret }: FieldProps) {
  const id = useId();
  if (secret) return <SecretField field={f} secret={secret} onSecret={onSecret} error={errors[f.name]} />;
  const set = value !== undefined;
  const Label = f.repeated ? "span" : "label";
  return (
    <div data-field={f.name}>
      <div className="mb-1.5 flex items-center gap-2">
        <Label htmlFor={f.repeated ? undefined : id} className="text-xs font-bold text-foreground/85">
          {f.label}
        </Label>
        {f.required && <span className="text-[10px] font-bold tracking-wide text-muted-foreground uppercase">required</span>}
        {set && !f.repeated && (
          <button type="button" aria-label={`Reset ${f.label}`} className="ml-auto cursor-pointer text-xs font-semibold text-primary hover:underline" onClick={() => onChange(undefined)}>
            Reset
          </button>
        )}
      </div>
      {f.kind === FormField_Kind.TABLE ? (
        <Table field={f} rows={Array.isArray(value) ? (value as JsonObject[]) : []} onChange={onChange} errors={errors} />
      ) : f.repeated ? (
        <List field={f} items={Array.isArray(value) ? value : []} onChange={onChange} errors={errors} />
      ) : (
        <Scalar id={id} field={f} value={value} onChange={onChange} />
      )}
      {f.help && <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{f.help}</p>}
      {f.choicesNote && <p className="mt-1 text-xs text-warn">{f.choicesNote}</p>}
      <FieldError message={errors[f.name]} />
    </div>
  );
}

function FieldError({ message }: { message?: string }) {
  return message ? <p className="mt-1.5 text-xs font-semibold text-down">{message}</p> : null;
}

const inputClass =
  "h-10 w-full rounded-lg border border-border bg-card px-3 text-sm text-foreground outline-none transition-colors placeholder:text-muted-foreground hover:border-foreground/25 focus:border-primary focus:ring-3 focus:ring-primary/15";

/** One string, int, bool or enum value; unset shows the default. */
function Scalar({ id, field: f, value, onChange, label }: { id?: string; field: FormField; value: JsonValue | undefined; onChange: (v: JsonValue | undefined) => void; label?: string }) {
  switch (f.kind) {
    case FormField_Kind.BOOL: {
      const on = value === undefined ? f.default === "true" : value === true;
      return <Switch id={id} label={label ?? f.label} checked={on} onChange={onChange} />;
    }
    case FormField_Kind.ENUM:
      return (
        <select id={id} aria-label={label} className={inputClass} value={typeof value === "string" ? value : ""} onChange={(e) => onChange(e.target.value || undefined)}>
          <option value="">{f.default ? `Default (${f.options.find((o) => o.value === f.default)?.label ?? f.default})` : "—"}</option>
          {f.options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      );
    default:
      if (f.choices.length > 0) return <Choices id={id} field={f} value={value} onChange={onChange} label={label} />;
      return (
        <input
          id={id}
          aria-label={label}
          type="text"
          inputMode={f.kind === FormField_Kind.INT ? "numeric" : undefined}
          className={inputClass}
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
function Choices({ id, field: f, value, onChange, label }: { id?: string; field: FormField; value: JsonValue | undefined; onChange: (v: JsonValue | undefined) => void; label?: string }) {
  const current = typeof value === "string" ? value : "";
  const offered = f.choices.some((c) => c.value === current);
  return (
    <select id={id} aria-label={label} className={inputClass} value={current} onChange={(e) => onChange(e.target.value || undefined)}>
      <option value="">{f.placeholder || "—"}</option>
      {current && !offered && <option value={current}>{`${current} (not on offer)`}</option>}
      {f.choices.map((c) => (
        <option key={c.value} value={c.value}>
          {c.label}
        </option>
      ))}
    </select>
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
        <input
          type="password"
          aria-label={f.label}
          autoComplete="off"
          className={inputClass}
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
    <fieldset data-field={f.name}>
      <legend className="mb-1.5 text-xs font-bold text-foreground/85">{f.label}</legend>
      {control}
      {f.help && <p className="mt-1 text-xs text-muted-foreground">{f.help}</p>}
      <FieldError message={error} />
    </fieldset>
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
              <X className="h-4 w-4" aria-hidden />
            </Button>
          </div>
          <FieldError message={errors[`${f.name}[${i}]`]} />
        </div>
      ))}
      <div>
        <Button size="sm" variant="outline" aria-label={`Add an item to ${f.label}`} onClick={() => onChange([...items, ""])}>
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
      <table aria-label={f.label} className="w-full text-sm">
        <thead>
          <tr className="text-left text-xs text-muted-foreground">
            {f.columns.map((c) => (
              <th key={c.name} className="pb-1 font-medium">
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
                  <td key={c.name} data-field={path} className="pr-2 pb-2">
                    <Scalar field={c} label={`${c.label}, row ${i + 1}`} value={row[c.name]} onChange={(v) => setCell(i, c.name, v)} />
                    <FieldError message={errors[path]} />
                  </td>
                );
              })}
              <td className="pb-2">
                <Button variant="ghost" aria-label={`Remove row ${i + 1}`} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
                  <X className="h-4 w-4" aria-hidden />
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div>
        <Button size="sm" variant="outline" aria-label={`Add a row to ${f.label}`} onClick={() => onChange([...rows, {}])}>
          Add row
        </Button>
      </div>
    </div>
  );
}

/** The config form in a dialog over the page (Catalogue variant 2). */
export function ConfigDialog({ owner, onClose }: { owner: string; onClose: () => void }) {
  const id = useId();
  return (
    <Dialog
      size="md"
      kicker="Configure"
      title={owner}
      subtitle={`Saving restarts ${owner} with the new config. Unset fields take their defaults.`}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={id}>
            Save and restart
          </Button>
        </FormActions>
      }
    >
      <ConfigForm owner={owner} formId={id} />
    </Dialog>
  );
}
