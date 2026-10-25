import { useState } from "react";
import { definePlugin } from "@ddash/plugin";
import { StatusDot } from "@ddash/ui";
import "./index.css";

function Hello() {
  const [n] = useState(1);
  return (
    <p className="hidden lg:flex text-up p-3 motion-safe:animate-[hello-pulse_2s_infinite]">
      <StatusDot tone="up" /> {n}
    </p>
  );
}

export default definePlugin({ nav: { order: 99 }, pages: [{ index: true, Component: Hello }] });
