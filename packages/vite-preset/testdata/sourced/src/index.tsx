import { definePlugin } from "@ddash/plugin";
import "./index.css";

const Page = () => <p className="hidden lg:flex text-up p-3" />;

export default definePlugin({ pages: [{ index: true, Component: Page }] });
