import * as Desktop from './desktopApi';
import type {
  AppearanceView,
  CommandToolchainCandidate,
  MCPServerConfigInput,
  MCPServerView,
  ModelProfileInput,
  ModelSettingsView,
  ProviderInput,
  WallpaperSelection,
  WorkspaceConfigInput,
  WorkspaceConfigView,
  WorkspaceOpenResult,
  WorkspaceState,
} from '../../bindings/github.com/afterlune/lune/internal/desktop/models';

export const defaultAppearance: AppearanceView = {
  theme: 'system', wallpaperType: 'static', wallpaperFile: '', wallpaperName: '', wallpaperURL: '', panelOpacity: 80,
};

type ProviderForm = ProviderInput & { hasAPIKey: boolean };
type MCPEnvironmentForm = { name: string; value: string; hasValue: boolean };
type MCPServerForm = Omit<MCPServerConfigInput, 'args' | 'env'> & { args: string[]; argsText: string; env: MCPEnvironmentForm[] };
type WorkspaceSettingsForm = Omit<WorkspaceConfigView, 'mcpServers' | 'commandToolchainPaths'> & {
  mcpServers: MCPServerForm[];
  commandToolchainPaths: string[];
};

type Dependencies = {
  previewMode: () => boolean;
  models: () => ModelSettingsView;
  setModels: (models: ModelSettingsView) => void;
  workspace: () => string;
  sessionID: () => string;
  workOutstanding: () => boolean;
  hasPendingRequests: () => boolean;
  appVisible: () => boolean;
  reducedMotion: () => boolean;
  setModal: (modal: 'settings' | 'mcp' | null) => void;
  clearError: () => void;
  showError: (error: unknown) => void;
  openWorkspace: (path: string) => Promise<void>;
  installWorkspaceState: (state: WorkspaceState) => void;
  completeWorkspaceOpen: (result: WorkspaceOpenResult) => Promise<void>;
};

export function createDesktopSettingsState(dependencies: Dependencies) {
  let theme = $state('system');
  let appearance = $state<AppearanceView>({ ...defaultAppearance });
  let appearanceDraft = $state<AppearanceView>({ ...defaultAppearance });
  let settingsTab = $state<'models' | 'appearance'>('models');
  let wallpaperCandidateFile = $state('');
  let wallpaperObjectURL = $state('');
  let wallpaperURL = $state('');
  let activeWallpaperType = $state('static');
  let wallpaperVideo = $state<HTMLVideoElement>();
  let providerForms = $state<ProviderForm[]>([]);
  let profileForms = $state<ModelProfileInput[]>([]);
  let defaultModel = $state('');
  let maxParallelAgents = $state(4);
  let modelSettingsSnapshot = $state('');
  let settingsSaving = $state(false);
  let mcpServers = $state<MCPServerView[]>([]);
  let mcpUpdatingServer = $state('');
  let workspaceSettingsForm = $state<WorkspaceSettingsForm>({
    contextWindow: 0, autoCompact: true, autoCompactThreshold: 0.8, reserveOutputTokens: 0,
    maxAttachmentFiles: 0, maxAttachmentBytes: 0, maxTotalBytes: 0, commandToolchainPaths: [], mcpServers: [],
  });
  let workspaceSettingsSaving = $state(false);
  let toolchainCandidates = $state<CommandToolchainCandidate[]>([]);
  let toolchainDetectionLoading = $state(false);

  const appearanceDirty = $derived(appearanceKey(appearanceDraft) !== appearanceKey(appearance));
  const modelSettingsDirty = $derived(serializeModelSettings() !== modelSettingsSnapshot);

  function applyTheme(value: string) {
    theme = value;
    document.documentElement.dataset.theme = value;
  }

  function applyAppearanceState(value: AppearanceView) {
    applyTheme(value.theme || 'system');
    activeWallpaperType = value.wallpaperType || 'static';
    wallpaperURL = value.wallpaperURL || '';
    document.documentElement.style.setProperty('--anime-panel-opacity', String((value.panelOpacity || 80) / 100));
  }

  function applyAppearance(value: AppearanceView) {
    appearance = value;
    appearanceDraft = { ...value };
    applyAppearanceState(value);
  }

  function appearanceKey(value: AppearanceView) {
    return JSON.stringify({
      theme: value.theme, wallpaperType: value.wallpaperType,
      wallpaperFile: value.wallpaperFile, wallpaperName: value.wallpaperName,
      panelOpacity: value.panelOpacity,
      wallpaperURL: value.wallpaperURL,
    });
  }

  function appearanceSettingsInput(value: AppearanceView) {
    return {
      theme: value.theme, wallpaperType: value.wallpaperType,
      wallpaperFile: value.wallpaperFile, wallpaperName: value.wallpaperName,
      panelOpacity: value.panelOpacity,
    };
  }

  function previewAppearanceDraft(value: AppearanceView) {
    appearanceDraft = { ...value };
    applyAppearanceState(appearanceDraft);
  }

  $effect(() => {
    const video = wallpaperVideo;
    if (!video) return;
    const play = theme === 'anime' && activeWallpaperType === 'dynamic' &&
      /\.(mp4|webm)(?:$|\?)/i.test(wallpaperURL) && dependencies.appVisible() && !dependencies.reducedMotion();
    if (play) void video.play().catch(() => {});
    else video.pause();
  });

  function modelSettingsInput() {
    return {
      providers: providerForms.map(({ name, protocol, baseURL, apiKey, serverCompaction }) => ({ name, protocol, baseURL, apiKey, serverCompaction })),
      profiles: profileForms.map(({ name, provider, model, maxOutputTokens, supportsInputAudio }) => ({ name, provider, model, maxOutputTokens, supportsInputAudio })),
      defaultModel, maxParallelAgents,
    };
  }

  function serializeModelSettings() {
    return JSON.stringify(modelSettingsInput());
  }

  function setSettingsForm() {
    const models = dependencies.models();
    providerForms = (models.providers ?? []).map((provider) => ({ ...provider, apiKey: '' }));
    profileForms = (models.profiles ?? []).map((profile) => ({ ...profile }));
    if (providerForms.length === 0) providerForms = [{ name: 'deepseek', protocol: 'responses', baseURL: 'https://api.deepseek.com', apiKey: '', hasAPIKey: false, serverCompaction: false }];
    if (profileForms.length === 0) profileForms = [{ name: 'default', provider: providerForms[0].name, model: '', maxOutputTokens: 0, supportsInputAudio: false }];
    defaultModel = models.defaultModel || profileForms[0]?.name || 'default';
    maxParallelAgents = models.maxParallelAgents || 4;
    modelSettingsSnapshot = serializeModelSettings();
    appearanceDraft = { ...appearance };
    settingsTab = 'models';
    wallpaperCandidateFile = '';
    if (wallpaperObjectURL && wallpaperObjectURL !== appearance.wallpaperURL) URL.revokeObjectURL(wallpaperObjectURL);
    wallpaperObjectURL = '';
    dependencies.setModal('settings');
  }

  function cancelSettings() {
    if (wallpaperCandidateFile && !dependencies.previewMode()) void Desktop.DiscardWallpaper(wallpaperCandidateFile).catch(() => {});
    if (wallpaperObjectURL && wallpaperObjectURL !== appearance.wallpaperURL) URL.revokeObjectURL(wallpaperObjectURL);
    wallpaperCandidateFile = '';
    wallpaperObjectURL = '';
    appearanceDraft = { ...appearance };
    applyAppearanceState(appearance);
    dependencies.setModal(null);
  }

  function setWallpaperType(type: 'static' | 'dynamic') {
    if (appearanceDraft.wallpaperType === type) return;
    if (wallpaperCandidateFile && !dependencies.previewMode()) void Desktop.DiscardWallpaper(wallpaperCandidateFile).catch(() => {});
    if (wallpaperObjectURL && wallpaperObjectURL !== appearance.wallpaperURL) URL.revokeObjectURL(wallpaperObjectURL);
    wallpaperCandidateFile = '';
    wallpaperObjectURL = '';
    previewAppearanceDraft({ ...appearanceDraft, wallpaperType: type, wallpaperFile: '', wallpaperName: '', wallpaperURL: '' });
  }

  function resetWallpaper() {
    if (wallpaperCandidateFile && !dependencies.previewMode()) void Desktop.DiscardWallpaper(wallpaperCandidateFile).catch(() => {});
    if (wallpaperObjectURL && wallpaperObjectURL !== appearance.wallpaperURL) URL.revokeObjectURL(wallpaperObjectURL);
    wallpaperCandidateFile = '';
    wallpaperObjectURL = '';
    previewAppearanceDraft({ ...appearanceDraft, wallpaperType: 'static', wallpaperFile: '', wallpaperName: '', wallpaperURL: '' });
  }

  async function chooseWallpaper() {
    if (dependencies.previewMode()) {
      document.getElementById('wallpaper-picker')?.click();
      return;
    }
    try {
      const selected: WallpaperSelection = await Desktop.PickWallpaper(appearanceDraft.wallpaperType);
      if (!selected.file) return;
      if (wallpaperCandidateFile && wallpaperCandidateFile !== appearance.wallpaperFile) {
        await Desktop.DiscardWallpaper(wallpaperCandidateFile);
      }
      wallpaperCandidateFile = selected.file;
      previewAppearanceDraft({
        ...appearanceDraft, wallpaperType: selected.type,
        wallpaperFile: selected.file, wallpaperName: selected.name, wallpaperURL: selected.url,
      });
    } catch (error) { dependencies.showError(error); }
  }

  function previewBrowserWallpaper(file: File) {
    const extension = file.name.toLowerCase().split('.').pop() || '';
    const allowed = appearanceDraft.wallpaperType === 'static'
      ? ['png', 'jpg', 'jpeg', 'webp'] : ['gif', 'mp4', 'webm'];
    if (!allowed.includes(extension)) { dependencies.showError('文件格式与所选壁纸类型不匹配'); return; }
    if (!file.size || file.size > 100 * 1024 * 1024) { dependencies.showError('壁纸文件不能超过 100 MiB'); return; }
    if (wallpaperObjectURL) URL.revokeObjectURL(wallpaperObjectURL);
    const url = URL.createObjectURL(file);
    wallpaperObjectURL = url;
    wallpaperCandidateFile = file.name;
    previewAppearanceDraft({ ...appearanceDraft, wallpaperFile: file.name, wallpaperName: file.name, wallpaperURL: url });
  }

  function workspaceSettingsFromView(config: WorkspaceConfigView): WorkspaceSettingsForm {
    return {
      contextWindow: config.contextWindow,
      autoCompact: config.autoCompact,
      autoCompactThreshold: config.autoCompactThreshold,
      reserveOutputTokens: config.reserveOutputTokens,
      maxAttachmentFiles: config.maxAttachmentFiles,
      maxAttachmentBytes: config.maxAttachmentBytes,
      maxTotalBytes: config.maxTotalBytes,
      commandToolchainPaths: [...(config.commandToolchainPaths ?? [])],
      mcpServers: (config.mcpServers ?? []).map((server) => ({
        name: server.name, originalName: server.name, transport: server.transport, command: server.command,
        args: server.args ?? [], argsText: (server.args ?? []).join('\n'),
        url: server.url, clientMetadataURL: server.clientMetadataURL,
        env: (server.env ?? []).map((item) => ({ name: item.name, value: '', hasValue: item.hasValue })),
      })),
    };
  }

  function addMCPServer() {
    const name = 'server-' + (workspaceSettingsForm.mcpServers.length + 1);
    workspaceSettingsForm.mcpServers = [...workspaceSettingsForm.mcpServers, {
      name, originalName: '', transport: 'stdio', command: '', args: [], argsText: '', url: '', clientMetadataURL: '', env: [],
    }];
  }

  function addMCPEnvironment(server: MCPServerForm) {
    server.env = [...server.env, { name: '', value: '', hasValue: false }];
  }

  function toolchainPathAuthorized(path: string) {
    return workspaceSettingsForm.commandToolchainPaths.some((item) => item.toLowerCase() === path.toLowerCase());
  }

  function addToolchainPath(path: string) {
    const normalized = path.trim();
    if (!normalized || toolchainPathAuthorized(normalized)) return;
    workspaceSettingsForm.commandToolchainPaths = [...workspaceSettingsForm.commandToolchainPaths, normalized];
  }

  function removeToolchainPath(path: string) {
    workspaceSettingsForm.commandToolchainPaths = workspaceSettingsForm.commandToolchainPaths.filter((item) => item.toLowerCase() !== path.toLowerCase());
  }

  async function detectCommandToolchains() {
    if (dependencies.previewMode() || toolchainDetectionLoading) return;
    toolchainDetectionLoading = true;
    try { toolchainCandidates = await Desktop.DetectCommandToolchains() ?? []; }
    catch (error) { dependencies.showError(error); }
    finally { toolchainDetectionLoading = false; }
  }

  async function pickCommandToolchainFolder() {
    if (dependencies.previewMode()) return;
    try {
      const path = await Desktop.PickCommandToolchainFolder();
      if (path) addToolchainPath(path);
    } catch (error) { dependencies.showError(error); }
  }

  function mcpStatus(name: string) {
    return mcpServers.find((server) => server.name === name);
  }

  async function saveWorkspaceSettings() {
    if (workspaceSettingsSaving || dependencies.workOutstanding() || dependencies.hasPendingRequests() || mcpUpdatingServer) return;
    workspaceSettingsSaving = true;
    try {
      const input: WorkspaceConfigInput = {
        contextWindow: workspaceSettingsForm.contextWindow,
        autoCompact: workspaceSettingsForm.autoCompact,
        autoCompactThreshold: workspaceSettingsForm.autoCompactThreshold,
        reserveOutputTokens: workspaceSettingsForm.reserveOutputTokens,
        maxAttachmentFiles: workspaceSettingsForm.maxAttachmentFiles,
        maxAttachmentBytes: workspaceSettingsForm.maxAttachmentBytes,
        maxTotalBytes: workspaceSettingsForm.maxTotalBytes,
        commandToolchainPaths: [...workspaceSettingsForm.commandToolchainPaths],
        mcpServers: workspaceSettingsForm.mcpServers.map((server) => {
          const stdio = server.transport === 'stdio';
          return {
            name: server.name, originalName: server.originalName, transport: server.transport,
            command: stdio ? server.command : '',
            args: stdio ? server.argsText.split(/\r?\n/).filter((argument) => argument.length > 0) : [],
            url: stdio ? '' : server.url, clientMetadataURL: stdio ? '' : server.clientMetadataURL,
            env: stdio ? server.env.filter((item) => item.name.trim()).map(({ name, value }) => ({ name, value })) : [],
          };
        }),
      };
      if (dependencies.previewMode()) {
        workspaceSettingsForm = { ...workspaceSettingsForm, mcpServers: workspaceSettingsForm.mcpServers.map((server) => ({ ...server, args: server.argsText.split(/\r?\n/).filter(Boolean) })) };
      } else {
        const saved = await Desktop.SaveWorkspaceConfig(input);
        workspaceSettingsForm = workspaceSettingsFromView(saved);
        mcpServers = await Desktop.ListMCPServers(dependencies.sessionID()) ?? [];
      }
      dependencies.clearError();
    } catch (error) { dependencies.showError(error); }
    finally { workspaceSettingsSaving = false; }
  }

  function addProvider() {
    providerForms = [...providerForms, { name: `provider-${providerForms.length + 1}`, protocol: 'responses', baseURL: 'https://api.openai.com/v1', apiKey: '', hasAPIKey: false, serverCompaction: false }];
  }

  function addProfile() {
    profileForms = [...profileForms, { name: `model-${profileForms.length + 1}`, provider: providerForms[0]?.name ?? '', model: '', maxOutputTokens: 0, supportsInputAudio: false }];
  }

  async function saveSettings() {
    if (settingsSaving) return;
    settingsSaving = true;
    try {
      if (appearanceKey(appearanceDraft) !== appearanceKey(appearance)) {
        if (dependencies.previewMode()) appearance = { ...appearanceDraft };
        else appearance = await Desktop.SaveAppearanceSettings(appearanceSettingsInput(appearanceDraft));
        appearanceDraft = { ...appearance };
        applyAppearanceState(appearance);
        wallpaperCandidateFile = '';
        wallpaperObjectURL = '';
      }
      if (serializeModelSettings() !== modelSettingsSnapshot) {
        const selectedSessionID = dependencies.sessionID();
        const input = modelSettingsInput();
        if (!dependencies.previewMode()) dependencies.setModels(await Desktop.SaveModelSettings(input));
        else dependencies.setModels({ ...dependencies.models(), configured: true, defaultModel });
        modelSettingsSnapshot = serializeModelSettings();
        const workspace = dependencies.workspace();
        if (workspace && !dependencies.previewMode()) {
          if (selectedSessionID) dependencies.installWorkspaceState(await Desktop.OpenSession(selectedSessionID));
          else await dependencies.completeWorkspaceOpen(await Desktop.OpenWorkspace(workspace));
        }
      }
      dependencies.setModal(null);
    } catch (error) { dependencies.showError(error); }
    finally { settingsSaving = false; }
  }

  async function changeTheme(value: string) {
    const next = { ...appearance, theme: value };
    applyAppearanceState(next);
    if (dependencies.previewMode()) { appearance = next; appearanceDraft = { ...next }; return; }
    try {
      await Desktop.SetTheme(value);
      appearance = next;
      appearanceDraft = { ...next };
    } catch (error) {
      applyAppearanceState(appearance);
      dependencies.showError(error);
    }
  }

  async function openFolderPicker() {
    if (dependencies.previewMode()) return;
    try {
      const path = await Desktop.PickWorkspace();
      if (path) await dependencies.openWorkspace(path);
    } catch (error) { dependencies.showError(error); }
  }

  async function openMCPManager() {
    if (!dependencies.workspace()) return;
    if (dependencies.previewMode()) {
      mcpServers = [];
      workspaceSettingsForm = {
        contextWindow: 0, autoCompact: true, autoCompactThreshold: 0.8, reserveOutputTokens: 0,
        maxAttachmentFiles: 0, maxAttachmentBytes: 0, maxTotalBytes: 0, commandToolchainPaths: [], mcpServers: [],
      };
      toolchainCandidates = [];
      dependencies.setModal('mcp');
      return;
    }
    try {
      workspaceSettingsForm = workspaceSettingsFromView(await Desktop.GetWorkspaceConfig());
      mcpServers = await Desktop.ListMCPServers(dependencies.sessionID()) ?? [];
      toolchainCandidates = await Desktop.DetectCommandToolchains() ?? [];
      dependencies.setModal('mcp');
    } catch (error) { dependencies.showError(error); }
  }

  async function setMCPServerEnabled(server: MCPServerView, enabled: boolean) {
    if (dependencies.workOutstanding() || dependencies.hasPendingRequests() || mcpUpdatingServer) return;
    mcpUpdatingServer = server.name;
    try {
      if (!dependencies.previewMode()) {
        await Desktop.SetMCPServerEnabled(dependencies.sessionID(), server.name, enabled);
        mcpServers = await Desktop.ListMCPServers(dependencies.sessionID()) ?? [];
      } else {
        mcpServers = mcpServers.map((item) => item.name === server.name ? { ...item, enabled, connected: enabled } : item);
      }
    } catch (error) { dependencies.showError(error); }
    finally { mcpUpdatingServer = ''; }
  }

  async function authenticateMCPServer(server: MCPServerView) {
    if (dependencies.workOutstanding() || dependencies.hasPendingRequests() || mcpUpdatingServer) return;
    mcpUpdatingServer = server.name;
    try {
      if (!dependencies.previewMode()) {
        await Desktop.AuthenticateMCPServer(dependencies.sessionID(), server.name);
        mcpServers = await Desktop.ListMCPServers(dependencies.sessionID()) ?? [];
      }
    } catch (error) { dependencies.showError(error); }
    finally { mcpUpdatingServer = ''; }
  }

  function dispose() {
    if (wallpaperObjectURL) URL.revokeObjectURL(wallpaperObjectURL);
  }

  return {
    get theme() { return theme; }, set theme(value: string) { theme = value; },
    get appearance() { return appearance; }, set appearance(value: AppearanceView) { appearance = value; },
    get appearanceDraft() { return appearanceDraft; }, set appearanceDraft(value: AppearanceView) { appearanceDraft = value; },
    get appearanceDirty() { return appearanceDirty; },
    get modelSettingsDirty() { return modelSettingsDirty; },
    get settingsTab() { return settingsTab; }, set settingsTab(value: 'models' | 'appearance') { settingsTab = value; },
    get wallpaperURL() { return wallpaperURL; }, set wallpaperURL(value: string) { wallpaperURL = value; },
    get activeWallpaperType() { return activeWallpaperType; }, set activeWallpaperType(value: string) { activeWallpaperType = value; },
    get wallpaperVideo() { return wallpaperVideo; }, set wallpaperVideo(value: HTMLVideoElement | undefined) { wallpaperVideo = value; },
    get providerForms() { return providerForms; }, set providerForms(value: ProviderForm[]) { providerForms = value; },
    get profileForms() { return profileForms; }, set profileForms(value: ModelProfileInput[]) { profileForms = value; },
    get defaultModel() { return defaultModel; }, set defaultModel(value: string) { defaultModel = value; },
    get maxParallelAgents() { return maxParallelAgents; }, set maxParallelAgents(value: number) { maxParallelAgents = value; },
    get settingsSaving() { return settingsSaving; }, set settingsSaving(value: boolean) { settingsSaving = value; },
    get mcpServers() { return mcpServers; }, set mcpServers(value: MCPServerView[]) { mcpServers = value; },
    get mcpUpdatingServer() { return mcpUpdatingServer; }, set mcpUpdatingServer(value: string) { mcpUpdatingServer = value; },
    get workspaceSettingsForm() { return workspaceSettingsForm; }, set workspaceSettingsForm(value: WorkspaceSettingsForm) { workspaceSettingsForm = value; },
    get workspaceSettingsSaving() { return workspaceSettingsSaving; }, set workspaceSettingsSaving(value: boolean) { workspaceSettingsSaving = value; },
    get toolchainCandidates() { return toolchainCandidates; }, set toolchainCandidates(value: CommandToolchainCandidate[]) { toolchainCandidates = value; },
    get toolchainDetectionLoading() { return toolchainDetectionLoading; }, set toolchainDetectionLoading(value: boolean) { toolchainDetectionLoading = value; },
    applyAppearance,
    previewAppearanceDraft,
    setSettingsForm,
    cancelSettings,
    setWallpaperType,
    resetWallpaper,
    chooseWallpaper,
    previewBrowserWallpaper,
    addMCPServer,
    addMCPEnvironment,
    toolchainPathAuthorized,
    addToolchainPath,
    removeToolchainPath,
    detectCommandToolchains,
    pickCommandToolchainFolder,
    mcpStatus,
    saveWorkspaceSettings,
    addProvider,
    addProfile,
    saveSettings,
    changeTheme,
    openFolderPicker,
    openMCPManager,
    setMCPServerEnabled,
    authenticateMCPServer,
    dispose,
  };
}
