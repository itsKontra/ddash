// Which surface a component sits on, for the few that draw differently on
// the hero. Internal: index.ts doesn't export it, so only HeroCard sets it.
import { createContext, useContext } from "react";

export const OnHero = createContext(false);

/** Whether the component is drawn inside a HeroCard. */
export const useOnHero = () => useContext(OnHero);
