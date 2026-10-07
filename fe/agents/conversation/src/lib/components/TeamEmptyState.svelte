<script lang="ts">
  /* The Team app with no Captain yet (draft-team-empty-state): what a Team
     is, the three things to know about the Captain role, and the two ways
     in. Nothing is created until the owner goes through the wizard.
     Also shown when the Captain is gone: the roster stays hidden until
     there is one again. candidates are the user's own Wick agents that can
     take the role; shared and remote agents never can, so someone with
     only those sees exactly the new-user page. */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import { REMOTE_NOT_CAPTAIN } from "../captainSettings.js";

  type Candidate = { id: string; name: string; handle: string };
  type Props = {
    onCreate: () => void;
    onRemote: () => void;
    candidates?: Candidate[];
    onMakeCaptain?: (c: Candidate) => void;
  };
  let { onCreate, onRemote, candidates = [], onMakeCaptain }: Props = $props();

  const STEPS = [
    { title: "Your first agent becomes the Captain", body: "Give it any name and its own persona — the Captain is a role, not a name." },
    { title: "The role can move", body: "Any time later: agent Settings → Captain → “Make Captain”. Names, chats and memory stay as they are." },
    { title: "You pick the provider & model", body: "Nothing is chosen for you — each agent gets the model you select when you create it." },
  ];
</script>

<div class="flex h-full items-center justify-center overflow-y-auto p-6" data-testid="team-empty">
  <div class="w-full max-w-xl text-center">
    <!-- Illustration: the Captain-to-be with two empty seats beside it. -->
    <div class="relative mx-auto mb-2 flex h-40 w-72 items-end justify-center gap-6 text-black-800 dark:text-black-600" aria-hidden="true">
      <span class="empty-seat mb-4 flex h-12 w-12 items-center justify-center rounded-full border-2 border-dashed border-white-300 dark:border-navy-600">+</span>
      <span class="relative">
        <AgentAvatar kind="blob" shape="circle" expression="neutral" color="#14b8a6" size={96} live />
        <span class="absolute -top-6 left-1/2 -translate-x-1/2 text-2xl">🧭</span>
      </span>
      <span class="empty-seat empty-seat-late mb-4 flex h-12 w-12 items-center justify-center rounded-full border-2 border-dashed border-white-300 dark:border-navy-600">+</span>
    </div>
    <h1 class="text-2xl font-semibold text-black-900 dark:text-white-100">{candidates.length ? "Your Team needs a Captain" : "Build your Team"}</h1>
    <p class="mx-auto mt-2 max-w-md text-sm text-black-800 dark:text-black-600">
      A Team is a set of agents you can chat with, @mention in Slack, and hand work between. Start with one — add specialists later.
    </p>
    <ol class="mx-auto mt-5 grid gap-2 text-left sm:grid-cols-3" data-testid="team-empty-steps">
      {#each STEPS as s, i (s.title)}
        <li class="flex gap-2.5 rounded-xl border border-white-300 bg-white-100 p-3 dark:border-navy-600 dark:bg-navy-800">
          <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-green-500 text-xs font-bold text-white-100">{i + 1}</span>
          <span class="min-w-0">
            <b class="block text-[13px] text-black-900 dark:text-white-100">{s.title}</b>
            <span class="mt-0.5 block text-xs text-black-800 dark:text-black-600">{s.body}</span>
          </span>
        </li>
      {/each}
    </ol>
    <div class="mt-5 flex flex-wrap justify-center gap-2">
      <button type="button" class="rounded-xl bg-green-500 px-4 py-2 text-sm font-semibold text-white-100 hover:bg-green-600" data-testid="team-empty-create" onclick={onCreate}>✨ Create your first agent</button>
      <button type="button" class="rounded-xl border border-white-300 bg-white-100 px-4 py-2 text-sm text-black-900 hover:bg-white-200 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100 dark:hover:bg-navy-600" data-testid="team-empty-remote" onclick={onRemote}>🔗 Connect a remote agent</button>
    </div>
    {#if candidates.length && onMakeCaptain}
      <div class="mt-4 flex flex-wrap items-center justify-center gap-2 text-sm text-black-800 dark:text-black-600" data-testid="team-empty-candidates">
        <span>Or pick one of your agents as Captain:</span>
        {#each candidates as c (c.id)}
          <button type="button" class="rounded-xl border border-white-300 bg-white-100 px-3 py-1.5 text-sm text-black-900 hover:bg-white-200 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100 dark:hover:bg-navy-600" onclick={() => onMakeCaptain(c)}>🧭 {c.name} <span class="text-black-700 dark:text-black-600">@{c.handle}</span></button>
        {/each}
      </div>
    {/if}
    <p class="mt-3 text-xs text-black-800 dark:text-black-600">{REMOTE_NOT_CAPTAIN}</p>
  </div>
</div>

<style>
  .empty-seat { animation: empty-seat-float 3.2s ease-in-out infinite; }
  .empty-seat-late { animation-delay: 0.6s; }
  @keyframes empty-seat-float {
    0%, 100% { transform: translateY(0); }
    50% { transform: translateY(-5px); }
  }
  @media (prefers-reduced-motion: reduce) {
    .empty-seat { animation: none; }
  }
</style>
