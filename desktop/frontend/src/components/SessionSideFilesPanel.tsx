import { useMemo, useState } from "react";
import { FilePlus2, FileText, ListPlus } from "lucide-react";
import { useT } from "../lib/i18n";
import { collectSessionSideFiles, formatReferenceListForPrompt, type SessionSideFile, type SessionSideItem } from "../lib/sessionSideFiles";

export type SideFilesDockVariant = "artifacts" | "references";

const INITIAL = 12;

/** tl1437: interactive row list for the artifacts dock variant — the gap list
 *  against the file tree (WorkspaceTreeMenu family) asked for row selection
 *  first: a click highlights the row (single selection, same semantics as the
 *  tree's preview selection), a right-click selects the right-clicked row
 *  (Explorer behavior) and opens the row menu (menu items land with the
 *  following commits). Without a tabId the list renders byte-for-byte the
 *  static rows — the accordion's shared FileList above is untouched, so the
 *  default files-mode view keeps its exact pre-tl1437 behavior. */
function DockArtifactList({ files, tabId }: { files: SessionSideFile[]; tabId?: string }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  // tl1437: the selected row path. Component-local like the tree's selection —
  // the artifacts list is a transcript projection, so nothing here persists.
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const visible = expanded ? files : files.slice(0, INITIAL);
  const hidden = files.length - visible.length;
  const interactive = Boolean(tabId);
  if (files.length === 0) return <p className="side-files__empty">{t("sideFiles.empty")}</p>;
  return (
    <>
      <ul className="side-files__list" role={interactive ? "listbox" : undefined} aria-label={interactive ? t("sideFiles.artifacts") : undefined}>
        {visible.map((f) => (
          <li
            key={f.path}
            className={interactive ? "side-files__item side-files__item--interactive" : "side-files__item"}
            title={interactive ? undefined : `${f.path} · ${f.via}`}
          >
            {interactive ? (
              <button
                type="button"
                role="option"
                aria-selected={selectedPath === f.path}
                className={`side-files__row${selectedPath === f.path ? " side-files__row--selected" : ""}`}
                data-side-file-path={f.path}
                title={`${f.path} · ${f.via}`}
                onClick={() => setSelectedPath(f.path)}
              >
                <FileText size={12} aria-hidden="true" />
                <span>{f.path}</span>
              </button>
            ) : (
              <>
                <FileText size={12} aria-hidden="true" />
                <span>{f.path}</span>
              </>
            )}
          </li>
        ))}
      </ul>
      {hidden > 0 && !expanded && (
        <div className="side-files__foot">
          <button type="button" className="btn btn--small" onClick={() => setExpanded(true)}>
            {t("sideFiles.showMore", { count: hidden })}
          </button>
        </div>
      )}
    </>
  );
}

function FileList({ files }: { files: SessionSideFile[] }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const visible = expanded ? files : files.slice(0, INITIAL);
  const hidden = files.length - visible.length;
  if (files.length === 0) return <p className="side-files__empty">{t("sideFiles.empty")}</p>;
  return (
    <>
      <ul className="side-files__list">
        {visible.map((f) => (
          <li key={f.path} className="side-files__item" title={`${f.path} · ${f.via}`}>
            <FileText size={12} aria-hidden="true" />
            <span>{f.path}</span>
          </li>
        ))}
      </ul>
      {hidden > 0 && !expanded && (
        <div className="side-files__foot">
          <button type="button" className="btn btn--small" onClick={() => setExpanded(true)}>
            {t("sideFiles.showMore", { count: hidden })}
          </button>
        </div>
      )}
    </>
  );
}

export interface SessionSideFilesPanelProps {
  items: readonly SessionSideItem[];
  /** Appends a reference-list block to the composer draft. */
  onInjectReferences?: (text: string) => void;
}

/**
 * Task 114: right-panel grouping of session-produced artifacts vs files the
 * session read (references), with a one-click context injection for refs.
 */
export function SessionSideFilesPanel({ items, onInjectReferences }: SessionSideFilesPanelProps) {
  const t = useT();
  const { artifacts, references } = useMemo(() => collectSessionSideFiles(items), [items]);
  const empty = artifacts.length === 0 && references.length === 0;
  const inject = () => {
    const block = formatReferenceListForPrompt(references);
    if (block && onInjectReferences) onInjectReferences(block);
  };
  return (
    <div className="side-files" aria-label={t("sideFiles.title")}>
      {empty ? (
        <p className="side-files__empty">{t("sideFiles.empty")}</p>
      ) : (
        <>
          <details className="side-files__group" open>
            <summary title={t("sideFiles.artifactsHint")}>
              <span>
                <FilePlus2 size={13} aria-hidden="true" /> {t("sideFiles.artifacts")}
              </span>
              <span className="side-files__meta">{t("sideFiles.artifactsMeta", { count: artifacts.length })}</span>
            </summary>
            <FileList files={artifacts} />
          </details>
          <details className="side-files__group" open>
            <summary title={t("sideFiles.referencesHint")}>
              <span>
                <FileText size={13} aria-hidden="true" /> {t("sideFiles.references")}
              </span>
              <span className="side-files__meta">{t("sideFiles.referencesMeta", { count: references.length })}</span>
            </summary>
            <FileList files={references} />
            {references.length > 0 && onInjectReferences && (
              <div className="side-files__foot">
                <button type="button" className="btn btn--secondary btn--small" onClick={inject}>
                  <ListPlus size={13} aria-hidden="true" />
                  <span>{t("sideFiles.injectReferences")}</span>
                </button>
              </div>
            )}
          </details>
        </>
      )}
    </div>
  );
}

/**
 * Task 260: single-variant dock view for the two session side-files tabs.
 * "artifacts" renders the write-path list, "references" the read-path list —
 * same aggregation as the combined panel above, one group per tab, with the
 * same empty state and show-more pacing. Read-only: the only action is the
 * references "inject into composer" button, same payload as task 114.
 *
 * tl1437: the artifacts variant swaps the static list for the interactive
 * DockArtifactList when a tabId is provided (App always provides one) — row
 * selection and the row menu close the gap against the file tree. Without a
 * tabId the artifacts body stays byte-for-byte the task-260 shape.
 */
export function SideFilesDockPanel({ items, variant, onInjectReferences, tabId }: {
  items: readonly SessionSideItem[];
  variant: SideFilesDockVariant;
  onInjectReferences?: (text: string) => void;
  /** tl1437: the active tab id — scope for the row menu's ForTab bridges
   *  (open probe / absolute-path resolve). Absent keeps the list static. */
  tabId?: string;
}) {
  const t = useT();
  const { artifacts, references } = useMemo(() => collectSessionSideFiles(items), [items]);
  if (variant === "artifacts") {
    return (
      <div className="side-files side-files--dock" aria-label={t("sideFiles.artifacts")}>
        {artifacts.length === 0 ? (
          <p className="side-files__empty">{t("sideFiles.empty")}</p>
        ) : (
          <details className="side-files__group" open>
            <summary title={t("sideFiles.artifactsHint")}>
              <span>
                <FilePlus2 size={13} aria-hidden="true" /> {t("sideFiles.artifacts")}
              </span>
              <span className="side-files__meta">{t("sideFiles.artifactsMeta", { count: artifacts.length })}</span>
            </summary>
            <DockArtifactList files={artifacts} tabId={tabId} />
          </details>
        )}
      </div>
    );
  }
  return (
    <div className="side-files side-files--dock" aria-label={t("sideFiles.references")}>
      {references.length === 0 ? (
        <p className="side-files__empty">{t("sideFiles.empty")}</p>
      ) : (
        <details className="side-files__group" open>
          <summary title={t("sideFiles.referencesHint")}>
              <span>
                <FileText size={13} aria-hidden="true" /> {t("sideFiles.references")}
              </span>
              <span className="side-files__meta">{t("sideFiles.referencesMeta", { count: references.length })}</span>
            </summary>
          <FileList files={references} />
          {onInjectReferences && (
            <div className="side-files__foot">
              <button
                type="button" className="btn btn--secondary btn--small"
                onClick={() => {
                  const block = formatReferenceListForPrompt(references);
                  if (block) onInjectReferences(block);
                }}
              >
                <ListPlus size={13} aria-hidden="true" />
                <span>{t("sideFiles.injectReferences")}</span>
              </button>
            </div>
          )}
        </details>
      )}
    </div>
  );
}
