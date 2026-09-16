import { ClipboardList } from "lucide-react";
import { useT } from "../lib/i18n";
import type { CompletionReportFields } from "../lib/completionReport";

const FIELD_ORDER: Array<{ key: keyof CompletionReportFields; labelKey: "completionReport.deliverables" | "completionReport.changes" | "completionReport.verification" | "completionReport.remaining" }> = [
  { key: "deliverables", labelKey: "completionReport.deliverables" },
  { key: "changes", labelKey: "completionReport.changes" },
  { key: "verification", labelKey: "completionReport.verification" },
  { key: "remaining", labelKey: "completionReport.remaining" },
];

/** Task 112: structured hand-off card rendered at the end of a finished turn. */
export function CompletionReportCard({ fields, id }: { fields: CompletionReportFields; id?: string }) {
  const t = useT();
  return (
    <div className="completion-report" data-entrance={id} role="note" aria-label={t("completionReport.title")}>
      <div className="completion-report__head">
        <ClipboardList size={14} aria-hidden="true" />
        <span>{t("completionReport.title")}</span>
      </div>
      <dl className="completion-report__fields">
        {FIELD_ORDER.map(({ key, labelKey }) => {
          const value = fields[key];
          return (
            <div key={key} className={`completion-report__row${value ? "" : " completion-report__row--empty"}`}>
              <dt>{t(labelKey)}</dt>
              <dd>{value || t("completionReport.none")}</dd>
            </div>
          );
        })}
      </dl>
    </div>
  );
}
