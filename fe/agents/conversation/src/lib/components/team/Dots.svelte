<script lang="ts">
  /* The typing cue after a working line ("waiting on 3 teammates•••"):
     three dots in currentColor that bounce in turn, as the mockup's .dots
     (4px, 2px apart, 1.2s, .15s/.3s delays, .5→1 and up 3px). Decorative;
     the line it follows already says it. No test id: a list can hold
     many, so tests find them by .dots. */
</script>

<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>

<style>
  .dots i {
    display: inline-block;
    width: 4px;
    height: 4px;
    margin-left: 2px;
    border-radius: 9999px;
    background: currentColor;
    animation: team-dot 1.2s infinite ease-in-out;
  }
  .dots i:nth-child(2) { animation-delay: 0.15s; }
  .dots i:nth-child(3) { animation-delay: 0.3s; }
  @keyframes team-dot {
    0%, 80%, 100% { opacity: 0.5; transform: translateY(0); }
    40% { opacity: 1; transform: translateY(-3px); }
  }
  @media (prefers-reduced-motion: reduce) {
    .dots i { animation: none; opacity: 0.7; }
  }
</style>
