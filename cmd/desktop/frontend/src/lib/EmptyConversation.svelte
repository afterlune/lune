<script lang="ts">
  import BrandMark from './BrandMark.svelte';
  import WelcomeArt from './WelcomeArt.svelte';
</script>

<section class="conversation-empty" aria-label="开始新对话">
  <WelcomeArt class="conversation-empty-art" />
  <div class="conversation-empty-orbit">
    <span class="empty-orbit-ring ring-one"></span>
    <span class="empty-orbit-ring ring-two"></span>
    <span class="conversation-empty-mark"><BrandMark size={26} /></span>
  </div>
  <h2>从一个问题开始</h2>
  <p>描述你想读、想改或想验证的事，Agent 会在当前工作区里接手。</p>
  <span class="conversation-empty-hint"><kbd>Ctrl K</kbd> 搜索已有会话</span>
</section>
