export type LampColor = "green" | "red" | "amber" | "off";

interface LampProps {
  color: LampColor;
  label: string;
  size?: "md" | "lg";
  /** Blink slowly, for something in progress. */
  pulse?: boolean;
}

/** A panel indicator lamp. The label is read by screen readers. */
export function Lamp({ color, label, size = "md", pulse = false }: LampProps) {
  const classes = ["lamp"];
  if (color !== "off") classes.push(`lamp--${color}`);
  if (size === "lg") classes.push("lamp--lg");
  if (pulse) classes.push("lamp--pulse");
  return <span className={classes.join(" ")} role="img" aria-label={label} title={label} />;
}

interface StatusProps {
  color: LampColor;
  children: string;
  size?: "md" | "lg";
}

/** A lamp with its word next to it: state is never color alone. */
export function Status({ color, children, size }: StatusProps) {
  return (
    <span className="status">
      <Lamp color={color} label={children} size={size} />
      <span>{children}</span>
    </span>
  );
}
