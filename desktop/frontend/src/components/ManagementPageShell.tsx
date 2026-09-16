import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";
import { ArrowLeft } from "lucide-react";
import { useManagementT } from "../lib/managementLocale";
import "./ManagementPageShell.css";

const DOUBLE_ESC_MS = 600;

/** Task 133: full-screen management pages return to the conversation on a
 *  quick double-Esc. A single Esc arms a brief "press again" affordance so
 *  an accidental keypress does not leave the session. */
export function ManagementPageShell({ title, description, actions, navigation, contentRef, children, active = true, onBack, className = "" }: {
  title: string; description?: string; actions?: ReactNode; navigation?: ReactNode; children: ReactNode;
  contentRef?: React.RefObject<HTMLElement | null>;
  active?: boolean; onBack: () => void; className?: string;
}) {
  const t = useManagementT();
  const backRef = useRef<HTMLButtonElement>(null);
  const lastEscAtRef = useRef(0);
  useLayoutEffect(() => { if (active) backRef.current?.focus({ preventScroll: true }); }, [active]);
  useEffect(() => {
    if (!active) {
      lastEscAtRef.current = 0;
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      // Let open menus/dialogs consume Esc first.
      if (event.defaultPrevented) return;
      const target = event.target;
      if (target instanceof HTMLElement) {
        const tag = target.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable) return;
        // An open details/summary or popover inside the page keeps its own Esc.
        if (target.closest("[role='dialog'], .modal, .reasonix-confirm-backdrop")) return;
      }
      const now = Date.now();
      if (now - lastEscAtRef.current <= DOUBLE_ESC_MS) {
        lastEscAtRef.current = 0;
        event.preventDefault();
        event.stopPropagation();
        onBack();
        return;
      }
      lastEscAtRef.current = now;
      // First Esc: show a brief hint on the back button without leaving.
      if (backRef.current) {
        backRef.current.classList.add("management-screen__back--esc-armed");
        window.setTimeout(() => backRef.current?.classList.remove("management-screen__back--esc-armed"), DOUBLE_ESC_MS);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [active, onBack]);
  const back = <button id="management-back" ref={backRef} className="management-screen__back" type="button" onClick={onBack}><ArrowLeft size={18} />{t("back")}</button>;
  return <section className={`management-screen ${className}`} hidden={!active} inert={!active} aria-label={title}>
    <header className="management-screen__chrome" aria-hidden="true" />
    {navigation ? <div className="settings-center"><aside className="settings-screen__sidebar">{back}{navigation}</aside><main ref={contentRef} className="settings-center__content">{children}</main></div> : <>
      <div className="management-screen__top">{back}<div className="management-screen__heading"><h1>{title}</h1>{description && <p>{description}</p>}</div><div className="management-screen__actions">{actions}</div></div>
      <main className="management-screen__content">{children}</main>
    </>}
  </section>;
}
