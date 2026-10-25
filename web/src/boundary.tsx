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

