// 任务 704/1112 — 轨迹视图入口：组件本体在 trajectoryViewBody.tsx（无 CSS
// 导入，可被 tsx 直跑的回归测试直接 import），本文件负责挂载 CSS 并 re-export。
// （直接在 body 文件 import CSS 会让 tsx 直跑的 harness 因未知扩展名崩溃。）
import "./TrajectoryView.css";

export { TrajectoryView, type TrajectoryViewProps, projectTrajectoryRows } from "./trajectoryViewBody";
