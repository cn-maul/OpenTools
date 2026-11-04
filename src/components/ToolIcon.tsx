type ToolIconProps = {
  icon?: string;
  label: string;
};

export default function ToolIcon({ icon, label }: ToolIconProps) {
  switch (icon) {
    case "braces":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M9 4.5H7.8A2.3 2.3 0 0 0 5.5 6.8v2.1c0 1.3-.7 2.2-2 2.6v1c1.3.4 2 1.3 2 2.6v2.1a2.3 2.3 0 0 0 2.3 2.3H9" />
          <path d="M15 4.5h1.2a2.3 2.3 0 0 1 2.3 2.3v2.1c0 1.3.7 2.2 2 2.6v1c-1.3.4-2 1.3-2 2.6v2.1a2.3 2.3 0 0 1-2.3 2.3H15" />
        </svg>
      );
    case "files":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M8 3.5h5.5l3.5 3.5v10.5A1.5 1.5 0 0 1 15.5 19H8a1.5 1.5 0 0 1-1.5-1.5v-12A2 2 0 0 1 8.5 3.5Z" />
          <path d="M13 3.8V7.5h3.8M5.5 7v11.2A2.8 2.8 0 0 0 8.3 21h6.4" />
        </svg>
      );
    case "network":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="5" r="2.2" />
          <circle cx="5" cy="18" r="2.2" />
          <circle cx="19" cy="18" r="2.2" />
          <path d="M12 7.2v3.4m0 0-5.3 5.2m5.3-5.2 5.3 5.2" />
        </svg>
      );
    case "system":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <rect x="3.5" y="4.5" width="17" height="12" rx="2" />
          <path d="M8 20h8M12 16.5V20M7 8h4m-4 3h7" />
        </svg>
      );
    case "lan":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <rect x="3.5" y="14.5" width="5" height="5" rx="1" />
          <rect x="15.5" y="14.5" width="5" height="5" rx="1" />
          <rect x="9.5" y="4.5" width="5" height="5" rx="1" />
          <path d="M12 9.5v2.7m-6.5 2.3v-2.3h13v2.3" />
        </svg>
      );
    case "printer":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M7 8V4.5h10V8" />
          <rect x="3.5" y="8" width="17" height="8.5" rx="2" />
          <path d="M7 13h10v6.5H7z" />
          <circle cx="17.5" cy="11" r=".7" />
        </svg>
      );
    case "disk":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="12" r="8.5" />
          <circle cx="12" cy="12" r="2.4" />
          <path d="M12 3.5v4m8.5 4.5h-4M12 20.5v-4m-8.5-4.5h4" />
        </svg>
      );
    case "crash":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M13 3 5.5 13.5H11L10 21l7.5-10.5H12z" />
        </svg>
      );
    case "gauge":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M4.5 17.5a8.5 8.5 0 1 1 15 0" />
          <path d="M12 17.5 16 11" />
          <circle cx="12" cy="17.5" r="1.4" />
        </svg>
      );
    case "uninstall":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <rect x="4.5" y="6.5" width="15" height="14" rx="2" />
          <path d="M9 6.5V4.8A1.3 1.3 0 0 1 10.3 3.5h3.4A1.3 1.3 0 0 1 15 4.8v1.7M9.8 11.5l4.4 4.4m0-4.4-4.4 4.4" />
        </svg>
      );
    case "startup":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M5 12.5a7.5 7.5 0 1 1 2.2 5.3" />
          <path d="M10 9.5l5 3-5 3z" />
        </svg>
      );
    case "document":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M6.5 3.5h7l4 4v13a1 1 0 0 1-1 1h-10a1 1 0 0 1-1-1v-16a1 1 0 0 1 1-1Z" />
          <path d="M13.5 3.8v4h4M9 12h6m-6 3h6m-6 3h4" />
        </svg>
      );
    default:
      return <span className="tool-icon-fallback">{label.slice(0, 1).toUpperCase()}</span>;
  }
}
