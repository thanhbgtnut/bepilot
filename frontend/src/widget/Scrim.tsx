interface ScrimProps {
  show: boolean;
  onClick: () => void;
}

export function Scrim({ show, onClick }: ScrimProps) {
  return (
    <div
      onClick={onClick}
      className={`fixed inset-0 z-45 bg-[rgba(10,20,16,0.35)] transition-opacity duration-200 md:hidden ${
        show ? "pointer-events-auto opacity-100" : "pointer-events-none opacity-0"
      }`}
    />
  );
}
