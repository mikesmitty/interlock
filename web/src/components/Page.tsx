import type { ReactNode } from "react";

interface PageProps {
  title: string;
  lead?: ReactNode;
  actions?: ReactNode;
  back?: ReactNode;
  children: ReactNode;
}

export function Page({ title, lead, actions, back, children }: PageProps) {
  return (
    <section className="page">
      {back}
      <header className="page__head">
        <h1>{title}</h1>
        {lead && <p>{lead}</p>}
        {actions && <div className="page__actions">{actions}</div>}
      </header>
      {children}
    </section>
  );
}

export function Empty({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="panel empty">
      <h2>{title}</h2>
      {children}
    </div>
  );
}
