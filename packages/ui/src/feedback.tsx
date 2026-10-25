// The feedback family: how a Page says something failed, is partial, is
// empty, or shows raw text.
import { useRef, useState, type ReactNode } from "react";
import { Button, StatusDot } from "./atoms";
import { Icon, type IconName } from "./icon";
import { cx, toneTint } from "./tone";

const noticeIcon = { info: "info", warn: "triangle-alert", down: "octagon-alert" } as const satisfies Record<string, IconName>;

/**
 * An inline message in its tone's tint: news (info), a partial or degraded
 * answer (warn), or a failure (down). A failure is an alert; the others are
 * polite status messages.
 */
export function Notice({ tone, title, children }: { tone: "info" | "warn" | "down"; title?: ReactNode; children: ReactNode }) {
  return (
    <div role={tone === "down" ? "alert" : "status"} className={cx("flex gap-3 rounded-ui px-4 py-3 text-sm", toneTint[tone])}>
      <span className="mt-0.5 flex">
        <Icon name={noticeIcon[tone]} />
      </span>
      <div className="min-w-0 flex-1">
        {title && <div className="font-semibold">{title}</div>}
        <div className={cx("text-foreground", !!title && "mt-0.5")}>{children}</div>
      </div>
    </div>
  );
}

/** Nothing to show yet, and what to do about it: e.g. a Component that isn't configured, with a link to configure it. */
export function EmptyState({ icon, title, children, action }: { icon?: IconName; title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center px-6 py-10 text-center">
      {icon && (
        <span className="mb-3 grid h-11 w-11 place-items-center rounded-ui bg-primary-tint text-primary">
          <Icon name={icon} size="lg" />
        </span>
      )}
      <p className="text-sm font-semibold">{title}</p>
      {children && <div className="mt-1 max-w-sm text-sm text-muted-foreground">{children}</div>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

/**
 * Raw text in monospace: a config block, a request, an error. Lines never
 * wrap; the block scrolls sideways (and takes focus, so a keyboard can
 * scroll it). With label it is a region named by it. copy adds a Copy
 * button that puts the text on the clipboard, e.g. a line to paste elsewhere.
 */
export function CodeBlock({ children, label, copy }: { children: ReactNode; label?: string; copy?: boolean }) {
  const pre = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  const block = (
    <pre
      ref={pre}
      tabIndex={0}
      {...(label && { role: "region", "aria-label": label })}
      className={cx(
        "max-h-80 min-w-0 overflow-x-auto overflow-y-auto rounded-ui-sm border border-border bg-muted p-3 font-mono text-xs leading-relaxed whitespace-pre text-foreground",
        copy && "pr-24",
      )}
    >
      {children}
    </pre>
  );
  if (!copy) return block;
  return (
    <div className="relative min-w-0">
      {block}
      <span className="absolute top-2 right-2">
        <Button
          size="sm"
          variant="outline"
          icon={copied ? "check" : "copy"}
          onClick={() => {
            void navigator.clipboard.writeText(pre.current?.textContent ?? "").then(() => setCopied(true));
          }}
        >
          {copied ? "Copied" : "Copy"}
        </Button>
      </span>
    </div>
  );
}

/** Where something would be (a Plugin's Page, a Widget, a form) when it can't be read or render: what failed, and the error. */
export function ErrorFrame({ title, error }: { title: string; error: Error }) {
  return (
    <div role="alert" className="rounded-ui border border-warn bg-card p-5 shadow-elevation">
      <div className="mb-3 flex items-center gap-2 font-semibold">
        <StatusDot tone="warn" />
        {title}
      </div>
      <CodeBlock>{error.message}</CodeBlock>
    </div>
  );
}
