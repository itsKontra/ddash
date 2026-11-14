import { encode } from "uqr";

/** The quiet zone around the code, in modules: the 4 the QR standard asks for. */
const QUIET = 4;

/**
 * text as a QR code, drawn in the browser as an SVG image named by label.
 * Scanners want dark modules on a light ground, so it is the foreground ink
 * on the card in a light Theme and the inverse in a dark one: dark on
 * light both ways.
 */
export function QrCode({ text, label }: { text: string; label: string }) {
  const { size, data } = encode(text, { ecc: "M", border: QUIET });
  let d = "";
  data.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) d += `M${x} ${y}h1v1h-1z`;
    }),
  );
  return (
    <svg
      role="img"
      aria-label={label}
      viewBox={`0 0 ${size} ${size}`}
      shapeRendering="crispEdges"
      className="aspect-square w-full max-w-72 rounded-ui-sm bg-card text-foreground dark:bg-foreground dark:text-card"
    >
      <path d={d} fill="currentColor" />
    </svg>
  );
}
