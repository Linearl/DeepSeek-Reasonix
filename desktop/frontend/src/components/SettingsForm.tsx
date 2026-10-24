import type { ReactNode } from "react";
import { Tooltip } from "./Tooltip";

export function SettingsSection({
  title,
  className,
  description,
  actions,
  children,
}: {
  title?: ReactNode;
  className?: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const hasHead = Boolean(title || description || actions);
  return (
    <section className={`settings-section${className ? ` ${className}` : ""}`}>
      {hasHead && (
        <div className="settings-section__head">
          <div>
            {title && <div className="settings-section__title">{title}</div>}
            {description && (
              <div className="settings-section__desc">
                <SettingsHint hint={description} />
              </div>
            )}
          </div>
          {actions && <div className="settings-section__actions">{actions}</div>}
        </div>
      )}
      <div className="settings-section__body">{children}</div>
    </section>
  );
}

/** 任务 1099 — 实验室 pane 内的功能族分组卡：把同一族的成员行（SettingsField）
 * 收进一个边界明显的卡片容器（边框 + 微背景 + 圆角 + 内边距），替代原来「细
 * 分割线 + 间距」的弱分组。纯视觉容器：不持有状态、不改写子行的徽章语义
 * （621：成员徽章仍挂在各自行的 label 上）、不触碰 rail 聚合徽章（774 合同）。
 * 首期试点 smartAutomation（梦境/决策模式/后台与续跑三族）；其余 feature 卡
 * 后续按需逐张迁移（775 渐进模式，登记一张删一张）。内部子行沿用
 * settings-field 样式——卡内行间保留细分割线，卡片边框即族边界。 */
export function LabSectionCard({
  title,
  hint,
  children,
}: {
  title: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="lab-section-card">
      <div className="lab-section-card__head">
        <div className="lab-section-card__title">{title}</div>
        {hint && <div className="lab-section-card__hint">{hint}</div>}
      </div>
      <div className="lab-section-card__body">{children}</div>
    </section>
  );
}

export function SettingsField({
  label,
  hint,
  icon,
  children,
  className,
  stacked = false,
}: {
  label: ReactNode;
  hint?: ReactNode;
  icon?: ReactNode;
  children: ReactNode;
  className?: string;
  stacked?: boolean;
}) {
  return (
    <div className={`settings-field${stacked ? " settings-field--stacked" : ""}${className ? ` ${className}` : ""}`}>
      <div className={`settings-field__copy${icon ? " settings-field__copy--icon" : ""}`}>
        {icon && <span className="settings-field__icon" aria-hidden="true">{icon}</span>}
        <div className="settings-field__copy-body">
          <div className="settings-field__label">{label}</div>
          {hint && (
            <div className="settings-field__hint">
              <SettingsHint hint={hint} />
            </div>
          )}
        </div>
      </div>
      <div className="settings-field__control">{children}</div>
    </div>
  );
}

function SettingsHint({ hint }: { hint: ReactNode }) {
  if (typeof hint === "string" || typeof hint === "number") {
    const label = String(hint);
    return (
      <Tooltip label={label} fill block className="settings-field__hint-tooltip">
        <span className="settings-field__hint-line">{label}</span>
      </Tooltip>
    );
  }
  return hint;
}
