<template>
  <!--
    TypingIndicator — shown in the assistant column the moment a message is sent,
    masking model time-to-first-token (the gap before the first streamed chunk).
    It's replaced by real content (thinking or text) as soon as it arrives.
    Opacity-only pulse, reduced-motion safe.
  -->
  <div
    class="flex items-center gap-1.5 py-1"
    role="status"
    aria-label="Assistant is responding"
  >
    <span class="dot" />
    <span
      class="dot"
      style="animation-delay: 0.18s"
    />
    <span
      class="dot"
      style="animation-delay: 0.36s"
    />
    <span class="sr-only">Assistant is responding…</span>
  </div>
</template>

<style scoped>
.dot {
  width: 6px;
  height: 6px;
  border-radius: 9999px;
  background: var(--color-foreground-muted);
  opacity: 0.3;
  animation: typing-dot 1.2s ease-in-out infinite;
}
@keyframes typing-dot {
  0%, 80%, 100% { opacity: 0.25; }
  40% { opacity: 0.85; }
}
@media (prefers-reduced-motion: reduce) {
  .dot {
    animation: none;
    opacity: 0.55;
  }
}
</style>
