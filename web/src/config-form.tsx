import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import { useAction, useQuery } from "@ddash/plugin";
import {
  ConfigErrorsSchema,
  CoreService,
  FormField_Kind,
  type ComponentStatus,
  type FormField,
  type GetConfigFormResponse,
} from "@ddash/proto/ddash/core/core_pb";
import type { SetupHint as SetupHintMessage } from "@ddash/proto/ddash/config/config_pb";
import { Button, cx, StatusDot, type Tone } from "@ddash/ui";
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
export function ConfigForm({ owner }: { owner: string }) {
  const { data, error, dataUpdatedAt } = useQuery(CoreService.method.getConfigForm, { owner });
  const [status, setStatus] = useState<ComponentStatus>();
  if (error && !data) return <ErrorFrame title={`${owner}'s config can't be read`} error={error} />;
  if (!data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <div>
      {data.setupHint && <SetupHint hint={data.setupHint} />}
      {/* A save refetches the form; start over from what is stored then. */}
      <Editor key={dataUpdatedAt} owner={owner} form={data} onSaved={setStatus} />
      {status && <Health status={status} />}
    </div>
  );
}

/** What the Operator does outside ddash to finish the setup, with a Copy button for its block. */
function SetupHint({ hint }: { hint: SetupHintMessage }) {
  const id = useId();
  const [copied, setCopied] = useState(false);
  return (
    <section aria-labelledby={id} className="mb-5 rounded-ui border border-border bg-muted p-3 text-sm">
      <h3 id={id} className="mb-1 font-medium">
        Setup
      </h3>
      <p className="text-muted-foreground">{hint.text}</p>
      {hint.block && (
        <div className="mt-2 flex items-start gap-2">
          <pre className="min-w-0 flex-1 overflow-x-auto rounded-ui border border-border bg-card p-2 text-xs whitespace-pre-wrap break-all">{hint.block}</pre>
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              void navigator.clipboard.writeText(hint.block).then(() => setCopied(true));
            }}
          >
            {copied ? "Copied" : "Copy"}
          </Button>
        </div>
      )}
    </section>
  );
}

function Health({ status }: { status: ComponentStatus }) {
  return (
    <p role="status" className="mt-4 flex items-center gap-2 text-sm">
      <StatusDot tone={toneOf(status.state)} />
      <span className="font-medium">{status.state}</span>
      {status.message && <span className="text-muted-foreground">{status.message}</span>}
    </p>
  );
}

const tones: Record<string, Tone> = { ok: "up", degraded: "warn", unconfigured: "warn", down: "down", crashed: "down" };
const toneOf = (state: string): Tone => tones[state] ?? "idle";

function Editor({ owner, form, onSaved }: { owner: string; form: GetConfigFormResponse; onSaved: (s: ComponentStatus | undefined) => void }) {
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
      <div className="flex justify-end">
        <Button type="submit" disabled={save.pending}>
          Save
        </Button>
      </div>
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
      <div className="mb-1 flex items-center gap-2">
        <Label htmlFor={f.repeated ? undefined : id} className="text-sm font-medium">
          {f.label}
        </Label>
        {f.required && <span className="text-xs text-muted-foreground">required</span>}
        {set && !f.repeated && (
          <button type="button" aria-label={`Reset ${f.label}`} className="ml-auto text-xs text-muted-foreground hover:underline" onClick={() => onChange(undefined)}>
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
      {f.help && <p className="mt-1 text-xs text-muted-foreground">{f.help}</p>}
      <FieldError message={errors[f.name]} />
    </div>
  );
}

function FieldError({ message }: { message?: string }) {
  return message ? <p className="mt-1 text-xs text-down">{message}</p> : null;
}

const inputClass = "h-9 w-full rounded-ui border border-border bg-card px-3 text-sm placeholder:text-muted-foreground";

/** One string, int, bool or enum value; unset shows the default. */
function Scalar({ id, field: f, value, onChange, label }: { id?: string; field: FormField; value: JsonValue | undefined; onChange: (v: JsonValue | undefined) => void; label?: string }) {
  switch (f.kind) {
    case FormField_Kind.BOOL: {
      const on = value === undefined ? f.default === "true" : value === true;
      return <Toggle id={id} label={label ?? f.label} on={on} onChange={onChange} />;
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

/** A switch, e.g. for a bool field. */
export function Toggle({ id, on, onChange, label }: { id?: string; on: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      onClick={() => onChange(!on)}
      className={cx("relative inline-flex h-5 w-9 cursor-pointer items-center rounded-full transition-colors", on ? "bg-primary" : "bg-muted")}
    >
      <span className={cx("inline-block h-4 w-4 rounded-full bg-card shadow transition-transform", on ? "translate-x-4.5" : "translate-x-0.5")} />
    </button>
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
      <legend className="mb-1 text-sm font-medium">{f.label}</legend>
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

/** The config form in a dialog over the page. */
export function ConfigDialog({ owner, onClose }: { owner: string; onClose: () => void }) {
  const id = useId();
  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center overflow-y-auto bg-foreground/40 p-4 sm:p-10" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={`${owner} config`}
        aria-describedby={id}
        className="w-full max-w-2xl rounded-ui border border-border bg-card p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.key === "Escape" && onClose()}
      >
        <div className="mb-5 flex items-start gap-3">
          <div>
            <h2 className="text-lg font-semibold">{owner}</h2>
            <p id={id} className="text-sm text-muted-foreground">
              Saving restarts {owner} with the new config. Unset fields take their defaults.
            </p>
          </div>
          <Button variant="ghost" className="ml-auto" aria-label="Close" onClick={onClose}>
            <X className="h-4 w-4" aria-hidden />
          </Button>
        </div>
        <ConfigForm owner={owner} />
      </div>
    </div>
  );
}
