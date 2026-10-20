import { PageHeader } from "@ddash/ui";
import { ChevronRight, ScrollText, type LucideIcon } from "lucide-react";
import { Link, Route, Routes } from "react-router";
import { AuditLog } from "./audit-log";

/** /settings/*: the core's settings and the Pages under them. */
export function Settings() {
  return (
    <Routes>
      <Route index element={<Overview />} />
      <Route path="audit-log" element={<AuditLog />} />
      <Route path="*" element={<PageHeader title="Not found" subtitle="Settings has no Page here." />} />
    </Routes>
  );
}

function Overview() {
  return (
    <div>
      <PageHeader title="Settings" />
      <ul className="divide-y divide-border rounded-ui border border-border bg-card">
        <Entry to="audit-log" icon={ScrollText} title="Audit Log" description="Every Action ddash took, kept for a year." />
      </ul>
    </div>
  );
}

function Entry({ to, icon: Icon, title, description }: { to: string; icon: LucideIcon; title: string; description: string }) {
  return (
    <li>
      <Link to={to} className="flex items-center gap-3 px-4 py-3 hover:bg-muted">
        <Icon className="h-5 w-5 text-muted-foreground" aria-hidden />
        <span>
          <span className="block font-medium">{title}</span>
          <span className="block text-sm text-muted-foreground">{description}</span>
        </span>
        <ChevronRight className="ml-auto h-4 w-4 text-muted-foreground" aria-hidden />
      </Link>
    </li>
  );
}
