"use client";

import { createContext, useContext, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { CloseIcon } from "./icons";

const SheetContext = createContext<{ onClose: () => void; locked: boolean }>({ onClose: () => {}, locked: false });

/** Close control for a sheet head's trailing slot; disabled while the sheet is locked. */
export function SheetClose() {
  const { onClose, locked } = useContext(SheetContext);
  return <button aria-label="Close" className="icon-button sheet-close" disabled={locked} onClick={onClose} type="button"><CloseIcon /></button>;
}

// Matches --t-panel, the CSS exit animation duration.
const exitMs = 180;
const focusable = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

/**
 * Modal surface: bottom sheet on narrow screens, centered card otherwise.
 * Traps focus, restores it to the opener, and plays an exit before unmounting.
 */
export function Sheet({ open, onClose, locked = false, label, titled = true, className = "", returnFocus, children }: {
  open: boolean;
  onClose: () => void;
  /** Blocks Escape, backdrop, and close button (an in-flight search). */
  locked?: boolean;
  label: string;
  /** Render `label` as the head title with a scrolling body. Off when the content lays out its own head, body and foot. */
  titled?: boolean;
  className?: string;
  /** Selector focused on close when the opener has left the page (e.g. replaced by results). */
  returnFocus?: string;
  children: ReactNode;
}) {
  const titleID = useId();
  const [mounted, setMounted] = useState(open);
  const [closing, setClosing] = useState(false);
  const panel = useRef<HTMLDivElement>(null);
  const opener = useRef<HTMLElement | null>(null);
  // How the sheet was last driven: focus returns with a visible ring only after keyboard use.
  const keyboard = useRef(false);

  if (open && (!mounted || closing)) {
    setMounted(true);
    setClosing(false);
  } else if (!open && mounted && !closing) {
    setClosing(true);
  }

  useEffect(() => {
    if (!closing) return;
    const timer = window.setTimeout(() => {
      setMounted(false);
      setClosing(false);
    }, exitMs);
    return () => window.clearTimeout(timer);
  }, [closing]);

  // Remember the last focus outside any sheet. The listener is still attached while this sheet commits,
  // so its own autofocus fires here too; ignore focus that lands inside a dialog.
  useEffect(() => {
    if (open) return;
    const remember = (event: FocusEvent) => {
      if (event.target instanceof HTMLElement && !event.target.closest('[aria-modal="true"]')) opener.current = event.target;
    };
    document.addEventListener("focusin", remember);
    return () => document.removeEventListener("focusin", remember);
  }, [open]);

  useEffect(() => {
    if (!open || !panel.current) return;
    document.body.classList.add("modal-open");
    const node = panel.current;
    // Hide everything outside the sheet from assistive tech and pointer/keyboard focus.
    const hidden: { element: HTMLElement; inert: boolean; ariaHidden: string | null }[] = [];
    for (let branch: HTMLElement = node; branch.parentElement && branch !== document.body; branch = branch.parentElement) {
      for (const sibling of branch.parentElement.children) {
        if (sibling === branch || !(sibling instanceof HTMLElement) || sibling.tagName === "SCRIPT") continue;
        hidden.push({ element: sibling, inert: sibling.inert, ariaHidden: sibling.getAttribute("aria-hidden") });
        sibling.inert = true;
        sibling.setAttribute("aria-hidden", "true");
      }
    }
    if (!node.contains(document.activeElement)) {
      (node.querySelector<HTMLElement>("[autofocus]") ?? node.querySelector<HTMLElement>(focusable) ?? node).focus({ preventScroll: true });
    }
    return () => {
      document.body.classList.remove("modal-open");
      for (const item of hidden) {
        item.element.inert = item.inert;
        if (item.ariaHidden === null) item.element.removeAttribute("aria-hidden");
        else item.element.setAttribute("aria-hidden", item.ariaHidden);
      }
      const target = opener.current?.isConnected ? opener.current : returnFocus ? document.querySelector<HTMLElement>(returnFocus) : null;
      // `focusVisible` is not in TypeScript's DOM lib yet; browsers without it ignore the option.
      target?.focus({ preventScroll: true, focusVisible: keyboard.current } as FocusOptions);
    };
  }, [open, returnFocus]);

  if (!mounted) return null;

  return (
    <div
      className={`sheet-layer${closing ? " is-closing" : ""}`}
      onKeyDownCapture={() => { keyboard.current = true; }}
      onMouseDown={() => !locked && onClose()}
      onPointerDownCapture={() => { keyboard.current = false; }}
    >
      <div
        aria-label={titled ? undefined : label}
        aria-labelledby={titled ? titleID : undefined}
        aria-modal="true"
        className={`sheet ${className}`}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.stopPropagation();
            if (!locked) onClose();
            return;
          }
          if (event.key !== "Tab" || !panel.current) return;
          const items = [...panel.current.querySelectorAll<HTMLElement>(focusable)].filter((item) => item.offsetParent !== null);
          if (!items.length) return;
          const first = items[0];
          const last = items[items.length - 1];
          if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last.focus();
          } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first.focus();
          }
        }}
        onMouseDown={(event) => event.stopPropagation()}
        ref={panel}
        role="dialog"
        tabIndex={-1}
      >
        <SheetContext value={{ onClose, locked }}>
          {titled ? (
            <>
              <header className="sheet-head">
                <h2 className="sheet-title" id={titleID}>{label}</h2>
                <SheetClose />
              </header>
              <div className="sheet-body">{children}</div>
            </>
          ) : children}
        </SheetContext>
      </div>
    </div>
  );
}
