// Which surface a component sits on, for the few that draw differently
// there. Internal: index.ts doesn't export them.
import { createContext, useContext } from "react";

/** Set by HeroCard only. */
export const OnHero = createContext(false);

/** Whether the component is drawn inside a HeroCard. */
export const useOnHero = () => useContext(OnHero);

/** Set by Panel around its body; an overlay resets it. */
export const InFlush = createContext(false);

/** Whether the component sits straight in a flush Panel's unpadded body. */
export const useInFlush = () => useContext(InFlush);
