import { StatusDot } from "@ddash/ui";
import { Component, type ReactNode } from "react";

/**
 * Catches whatever a Plugin's Page or badge throws while rendering, so one
 * Plugin can't take the shell down. A new resetKey (the path) tries again.
 */
export class ErrorBoundary extends Component<
  { fallback: (error: Error) => ReactNode; resetKey?: string; children: ReactNode },
  { error: Error | null; key?: string }
> {
  state: { error: Error | null; key?: string } = { error: null };

  static getDerivedStateFromError(error: unknown) {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  static getDerivedStateFromProps(props: { resetKey?: string }, state: { error: Error | null; key?: string }) {
    return props.resetKey !== state.key ? { error: null, key: props.resetKey } : null;
  }

  render() {
    return this.state.error ? this.props.fallback(this.state.error) : this.props.children;
  }
}

/** Where a Plugin's Page would be, when it can't render. */
export function ErrorFrame({ title, error }: { title: string; error: Error }) {
  return (
    <div role="alert" className="rounded-ui border border-warn bg-card p-5">
      <div className="flex items-center gap-2 font-semibold">
        <StatusDot tone="warn" />
        {title}
      </div>
      <pre className="mt-2 text-sm whitespace-pre-wrap text-muted-foreground">{error.message}</pre>
    </div>
  );
}
