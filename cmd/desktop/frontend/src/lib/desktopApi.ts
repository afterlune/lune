import * as native from '../../bindings/github.com/afterlune/lune/internal/desktop/desktopservice';
export type DesktopApi = { [K in keyof typeof native]: (...args: Parameters<typeof native[K]>) => Promise<Awaited<ReturnType<typeof native[K]>>> };
let implementation: DesktopApi = native;
export function installDesktopApi(api: Partial<DesktopApi>): () => void {
  const previous = implementation; implementation = {...native,...api}; return () => { implementation=previous; };
}
const gateway = new Proxy({} as DesktopApi, {
  get(_target,key:keyof DesktopApi) { return (...args:unknown[]) => (implementation[key] as (...args:unknown[])=>Promise<unknown>)(...args); },
});
export const { AnswerQuestion, AuthenticateMCPServer, Bootstrap, CancelQueuedTurn, CloseWorkspace, CreateSession, DeleteSession, DetectCommandToolchains, DiscardWallpaper, EditUserMessage, ForgetDesktopLocation, GetWorkspaceConfig, ImplementPlan, Interrupt, ListDesktopProcesses, ListMCPServers, ListPendingSessions, OpenMCPURL, OpenSession, OpenWorkspace, PickCommandToolchainFolder, PickWallpaper, PickWorkspace, ReadDesktopProcessOutput, RecoverModel, RefreshSession, ResolveApproval, ResolveToolRecovery, ResolveUserInteraction, RestorePlanMode, SaveAppearanceSettings, SaveModelSettings, SaveWorkspaceConfig, SearchWorkspaceConversation, SetMCPServerEnabled, SetPermissionMode, SetPlanMode, SetSessionModel, SetSessionTitle, SetTheme, StopDesktopProcess, Submit } = gateway;
