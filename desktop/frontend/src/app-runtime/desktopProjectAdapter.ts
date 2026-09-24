import { app } from "../lib/bridge";

export const desktopProjectAdapter = {
  renameLocal: (id: string, title: string) => app.RenameTopic(id, title),
  // [type-bridge, 2026-09-24] wails regenerated models.ts: title/turns became
  // optional while the handwritten remoteTypes keeps them required — fill the
  // two required fields so the generated class still satisfies the ports type.
  listRemote: async (host: string, workspace: string) =>
    (await app.RemoteProjectSessions(host, workspace)).map(s => ({
      ...s, title: s.title ?? "", turns: s.turns ?? 0,
    })),
  renameRemote: (host: string, workspace: string, name: string, title: string) => app.RenameRemoteProjectSession(host, workspace, name, title),
};
