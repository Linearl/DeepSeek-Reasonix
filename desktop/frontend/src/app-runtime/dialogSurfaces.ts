// "Which surface is open" markers for the project tree and the takeover flows
// (task 38 batch B4). Each is a plain open/closed flag - a dialog reads it to
// decide whether to mount and nothing else, so they belong together rather than
// scattered through the monolith.
import { useState } from "react";

export function useDialogSurfaceOwner() {
  const [tasksOpen, setTasksOpen] = useState<false | "session" | "all">(false);
  const [takeoverDialogTab, setTakeoverDialogTab] = useState<string | null>(null);
  const [questionSearchOpen, setQuestionSearchOpen] = useState(false);
  const [reclaimBusyTab, setReclaimBusyTab] = useState<string | null>(null);

  return {
    tasksOpen,
    setTasksOpen,
    takeoverDialogTab,
    setTakeoverDialogTab,
    questionSearchOpen,
    setQuestionSearchOpen,
    reclaimBusyTab,
    setReclaimBusyTab,
  };
}
