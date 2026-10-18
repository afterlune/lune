<script lang="ts">
import Icon from './Icon.svelte';
import type { DesktopState } from './desktopState.svelte';
let { desktop }: { desktop:DesktopState } = $props();
</script>

      <div class="modal settings-modal" role="dialog" aria-modal="true" aria-labelledby="settings-title" tabindex="-1">
        <div class="modal-header"><div><span class="eyebrow">PREFERENCES</span><h2 id="settings-title">设置</h2></div><div class="modal-header-actions">{#if desktop.workspace}<button class="small-button" onclick={() => void desktop.openMCPManager()}>项目设置</button>{/if}<button class="icon-button" aria-label="关闭" onclick={desktop.cancelSettings}><Icon name="close" size={16} /></button></div></div>
        <div class="modal-body settings-body">
          <div class="settings-tabs" role="tablist" aria-label="设置分类">
            <button role="tab" aria-selected={desktop.settingsTab === 'models'} class:active={desktop.settingsTab === 'models'} onclick={() => desktop.settingsTab = 'models'}><Icon name="settings" size={16} /> 模型与提供方</button>
            <button role="tab" aria-selected={desktop.settingsTab === 'appearance'} class:active={desktop.settingsTab === 'appearance'} onclick={() => desktop.settingsTab = 'appearance'}><Icon name="sun" size={16} /> 外观与主题</button>
          </div>
          <div class="settings-content">
            {#if desktop.settingsTab === 'models'}
              <section class="settings-section"><div class="settings-section-heading"><div><h3>模型提供方</h3><p>API 密钥使用当前 Windows 用户 DPAPI 加密保存。</p></div><button class="small-button" onclick={desktop.addProvider}><Icon name="plus" size={13} /> 添加提供方</button></div>
                {#each desktop.providerForms as provider, index (index)}
                  <div class="provider-form">
                    <div class="form-grid">
                      <label>名称<input bind:value={provider.name} /></label>
                      <label>协议<select bind:value={provider.protocol}><option value="responses">Responses API</option><option value="anthropic-messages">Anthropic Messages</option></select></label>
                      <label class="wide-field">API 地址<input bind:value={provider.baseURL} placeholder="https://api.example.com/v1" /></label>
                      <label class="wide-field">API 密钥<input type="password" bind:value={provider.apiKey} placeholder={provider.hasAPIKey ? '留空则保留已保存的密钥' : '粘贴 API 密钥'} autocomplete="new-password" /></label>
                      <label class="check-label"><input type="checkbox" bind:checked={provider.serverCompaction} /> 服务端上下文压缩</label>
                    </div>
                    {#if desktop.providerForms.length > 1}<button class="remove-row" onclick={() => desktop.providerForms = desktop.providerForms.filter((_, i) => i !== index)}>移除提供方</button>{/if}
                  </div>
                {/each}
              </section>
              <section class="settings-section"><div class="settings-section-heading"><div><h3>模型档案</h3><p>档案名称会显示在会话的模型选择器中。</p></div><button class="small-button" onclick={desktop.addProfile}><Icon name="plus" size={13} /> 添加模型</button></div>
                {#each desktop.profileForms as profile, index (index)}
                  <div class="profile-form">
                    <label>档案名称<input bind:value={profile.name} /></label>
                    <label>提供方<select bind:value={profile.provider}>{#each desktop.providerForms as provider}<option value={provider.name}>{provider.name}</option>{/each}</select></label>
                    <label>模型 ID<input bind:value={profile.model} placeholder="例如 deepseek-chat" /></label>
                    <label>最大输出 Token<input type="number" min="0" bind:value={profile.maxOutputTokens} /></label>
                    {#if desktop.profileForms.length > 1}<button class="remove-row" onclick={() => desktop.profileForms = desktop.profileForms.filter((_, i) => i !== index)}>移除模型</button>{/if}
                  </div>
                {/each}
                <div class="form-grid bottom-grid"><label>默认模型<select bind:value={desktop.defaultModel}>{#each desktop.profileForms as profile}<option value={profile.name}>{profile.name}</option>{/each}</select></label><label>并行 Agent 数<input type="number" min="1" max="32" bind:value={desktop.maxParallelAgents} /></label></div>
              </section>
            {:else}
              <section class="appearance-settings">
                <div class="settings-section-heading"><div><h3>应用主题</h3><p>二次元主题只在此处启用；顶部快捷切换仅包含浅色和深色。</p></div></div>
                <div class="theme-cards" role="group" aria-label="应用主题">
                  <button class:chosen={desktop.appearanceDraft.theme === 'system'} class="theme-card" onclick={() => desktop.previewAppearanceDraft({ ...desktop.appearanceDraft, theme: 'system' })}><span class="theme-preview theme-preview-system" aria-hidden="true"><i class="tp-side"></i><span class="tp-main"><i></i><i></i><b></b></span></span><strong>跟随系统</strong></button>
                  <button class:chosen={desktop.appearanceDraft.theme === 'light'} class="theme-card" onclick={() => desktop.previewAppearanceDraft({ ...desktop.appearanceDraft, theme: 'light' })}><span class="theme-preview theme-preview-light" aria-hidden="true"><i class="tp-side"></i><span class="tp-main"><i></i><i></i><b></b></span></span><strong>浅色</strong></button>
                  <button class:chosen={desktop.appearanceDraft.theme === 'dark'} class="theme-card" onclick={() => desktop.previewAppearanceDraft({ ...desktop.appearanceDraft, theme: 'dark' })}><span class="theme-preview theme-preview-dark" aria-hidden="true"><i class="tp-side"></i><span class="tp-main"><i></i><i></i><b></b></span></span><strong>深色</strong></button>
                  <button class:chosen={desktop.appearanceDraft.theme === 'anime'} class="theme-card anime-card" onclick={() => desktop.previewAppearanceDraft({ ...desktop.appearanceDraft, theme: 'anime' })}><span class="theme-preview theme-preview-anime" aria-hidden="true"><i class="tp-side"></i><span class="tp-main"><i></i><i></i><b></b></span></span><strong>二次元</strong></button>
                </div>

                <div class="appearance-subsection">
                  <div class="settings-section-heading"><div><h3>二次元背景</h3><p>壁纸保存在本机，不会上传或同步。</p></div></div>
                  <div class="wallpaper-mode-switch" role="group" aria-label="壁纸类型">
                    <button class:selected={desktop.appearanceDraft.wallpaperType === 'static'} aria-pressed={desktop.appearanceDraft.wallpaperType === 'static'} onclick={() => desktop.setWallpaperType('static')}><Icon name="image" size={14} /> 静态壁纸</button>
                    <button class:selected={desktop.appearanceDraft.wallpaperType === 'dynamic'} aria-pressed={desktop.appearanceDraft.wallpaperType === 'dynamic'} onclick={() => desktop.setWallpaperType('dynamic')}><Icon name="process" size={14} /> 动态壁纸</button>
                  </div>
                  <input id="wallpaper-picker" type="file" accept={desktop.appearanceDraft.wallpaperType === 'static' ? 'image/png,image/jpeg,image/webp' : 'image/gif,video/mp4,video/webm'} hidden onchange={(event) => { const file = event.currentTarget.files?.[0]; if (file) desktop.previewBrowserWallpaper(file); event.currentTarget.value = ''; }} />
                  <div class="wallpaper-editor">
                    <div class="wallpaper-preview" aria-label="壁纸预览">
                      {#if desktop.appearanceDraft.wallpaperURL && desktop.appearanceDraft.wallpaperType === 'dynamic' && desktop.reducedMotion}
                        <div class="wallpaper-empty-preview"><span>Ⅱ</span><strong>已根据系统设置暂停动态预览</strong></div>
                      {:else if desktop.appearanceDraft.wallpaperURL && desktop.appearanceDraft.wallpaperType === 'dynamic' && /\.(mp4|webm)(?:$|\?)/i.test(desktop.appearanceDraft.wallpaperURL)}
                        <video src={desktop.appearanceDraft.wallpaperURL} autoplay={desktop.appVisible} muted loop playsinline></video>
                      {:else if desktop.appearanceDraft.wallpaperURL}
                        <img src={desktop.appearanceDraft.wallpaperURL} alt="当前壁纸预览" />
                      {:else if desktop.appearanceDraft.wallpaperFile}
                        <div class="wallpaper-empty-preview"><span>!</span><strong>找不到本地壁纸，请重新选择</strong></div>
                      {:else}
                        <div class="wallpaper-empty-preview"><Icon name="flower" size={22} /><strong>本地壁纸预览</strong></div>
                      {/if}
                    </div>
                    <div class="wallpaper-controls">
                      <button class="small-button" onclick={() => void desktop.chooseWallpaper()}><Icon name="image" size={13} /> 更换壁纸</button>
                      <strong class="wallpaper-filename" title={desktop.appearanceDraft.wallpaperName || desktop.appearanceDraft.wallpaperFile || '未选择壁纸'}>{desktop.appearanceDraft.wallpaperName || desktop.appearanceDraft.wallpaperFile || '未选择壁纸'}</strong>
                      <p>静态图：PNG / JPG / WebP<br />动态壁纸：GIF / MP4 / WebM</p>
                      <button class="quiet-button reset-wallpaper" onclick={desktop.resetWallpaper}>恢复默认背景</button>
                    </div>
                  </div>
                </div>

                <div class="appearance-adjustments">
                  <div class="opacity-control">
                    <label for="panel-opacity"><strong>面板透明度</strong><output>{desktop.appearanceDraft.panelOpacity}%</output></label>
                    <input id="panel-opacity" type="range" min="60" max="95" step="1" value={desktop.appearanceDraft.panelOpacity} oninput={(event) => desktop.previewAppearanceDraft({ ...desktop.appearanceDraft, panelOpacity: Number(event.currentTarget.value) })} />
                    <p>只调整侧边栏、会话区和对话框前景面板，壁纸保持原亮度。</p>
                  </div>
                  <div class="glass-preview"><strong>毛玻璃预览</strong><span>在当前壁纸下查看前景面板效果</span><div><b>会话内容</b><small>玻璃面板保持文字清晰</small></div></div>
                </div>
                <p class="appearance-save-note">壁纸和透明度会立即预览；保存后应用，取消会还原到之前设置。</p>
              </section>
            {/if}
          </div>
        </div>
        <div class="modal-footer"><button class="quiet-button" onclick={desktop.cancelSettings}>取消</button><button class="primary-button" disabled={desktop.settingsSaving || (!desktop.appearanceDirty && !desktop.modelSettingsDirty)} onclick={() => void desktop.saveSettings()}>{desktop.settingsSaving ? '正在保存…' : '保存设置'}</button></div>
      </div>
    
