<template>
  <!--
    BrandLockup — the mark plus the wordmark, at one size, in one place.

    The mobile top bar and the sidebar drawer header are both on screen on a
    phone (the drawer slides over the bar), so any difference between them
    reads as the logo changing size while you navigate. They previously drifted:
    the bar drew the mark at 24 px with an 8 px gap, the drawer at 32 px with a
    12 px gap and a larger wordmark. Rendering both from here means they cannot
    disagree again.

    Sized in between the two former values: large enough to hold the f(x)
    counter-shape at a glance, small enough to sit inside the 56 px bar with
    real breathing room above and below.
  -->
  <div class="flex items-center gap-2.5 text-foreground-strong font-mono">
    <OrvaLogo class="w-7 h-7 shrink-0" />
    <span class="font-bold tracking-tight text-base leading-none">Orva</span>
  </div>
</template>

<script setup>
import OrvaLogo from '@/components/OrvaLogo.vue'
</script>
