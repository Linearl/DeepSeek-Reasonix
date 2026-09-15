# data-analytics

Structured quantitative analysis: quality checks → diagnostics → KPI design → charts → recommendations.

## When to use
User asks for data quality, metric diagnostics, KPI design, dashboards, notebooks, or evidence-backed business analysis.

## How
1. **Quality**: missing / duplicates / outliers / unit consistency.
2. **Diagnose**: decompose metric (volume × conversion × price…), segment, time series.
3. **KPI**: definition, grain, owner, target, and caveats.
4. **Evidence**: every recommendation ties to a number and a table/chart.
5. Deliver as markdown report or notebook artifacts under `report/` or `tasks/`.

## Notes for Reasonix
- Pure process skill (md flow); no extra runtime required.
- Prefer pandas/openpyxl or `dp_*` scripts already in `scripts/` when present.
- State assumptions; do not hide data gaps.
