type ToolIconProps = {
  icon?: string;
  label: string;
};

// The framework knows nothing about individual tools. An icon value of
// "svg:<path>|<path>" renders the given SVG path data inside a 24x24
// viewBox; any other value (or an unparseable one) falls back to the first
// letter of the tool name.
export default function ToolIcon({ icon, label }: ToolIconProps) {
  if (typeof icon === "string" && icon.startsWith("svg:")) {
    const paths = icon.slice(4).split("|").filter(Boolean);
    if (paths.length > 0) {
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          {paths.map((d) => (
            <path key={d} d={d} />
          ))}
        </svg>
      );
    }
  }
  return <span className="tool-icon-fallback">{label.slice(0, 1).toUpperCase()}</span>;
}
