// PROTOTYPE: three UI shell variants on one page, switchable via ?variant=A|B|C.
import { useState, type ReactNode } from "react";
import VariantA from "./variants/A";
import VariantB from "./variants/B";
import VariantC from "./variants/C";
import { CataloguePage, PluginPage, SettingsPage, Switcher, UiKitPage, useTheme } from "./core";

export type ShellProps = { page: string; setPage: (p: string) => void; renderCorePage: (p: string) => ReactNode };

const param = (k: string, d: string) => new URLSearchParams(location.search).get(k) ?? d;
const setParam = (k: string, v: string) => {
  const u = new URL(location.href);
  u.searchParams.set(k, v);
  history.replaceState(null, "", u);
};

export default function App() {
  const [variant, setVariant] = useState(param("variant", "A"));
  const [page, setPageState] = useState(param("page", "home"));
  const t = useTheme();
  const setPage = (p: string) => {
    setPageState(p);
    setParam("page", p);
  };
  const renderCorePage = (p: string) =>
    p === "catalogue" ? <CataloguePage /> : p === "settings" ? <SettingsPage t={t} /> : p === "uikit" ? <UiKitPage /> : <PluginPage id={p} />;
  const props = { page, setPage, renderCorePage };
  return (
    <>
      {variant === "A" && <VariantA {...props} />}
      {variant === "B" && <VariantB {...props} />}
      {variant === "C" && <VariantC {...props} />}
      {import.meta.env.DEV && (
        <Switcher
          current={variant}
          onChange={(v) => {
            setVariant(v);
            setParam("variant", v);
          }}
        />
      )}
    </>
  );
}
