<script lang="ts">
  /* A roster row's avatar: live like the header's (the same agent in the
     same state moves the same way in both; rows scrolled away pause), with
     the remote / alert ring around it while another chat is busy or waits
     on you (AvatarActivity, rings mode). One animation per state: while
     the dashed remote ring shows, the avatar's own remote cue (the classic
     avatar's orbit dot) is off. The open main chat's row keeps the
     avatar's cues and gets no ring, its thread already shows the typing. */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import AvatarActivity from "./AvatarActivity.svelte";
  import { rosterActivity } from "../../avatarActivity.js";
  import type { AgentItem } from "../../api/team.js";
  import { avatarWorking, type RosterStatus } from "../../rosterStatus.js";

  type Props = {
    agent: AgentItem;
    st: RosterStatus;
    /** This agent's main chat is the one on screen (mainChatOpen). */
    open: boolean;
    hatching?: boolean;
    /** Idle since before today: asleep like a disabled agent (dozing). */
    dozing?: boolean;
  };
  let { agent: a, st, open, hatching = false, dozing = false }: Props = $props();

  const activity = $derived(rosterActivity(st, open));
  const ring = $derived(activity === "remote");
</script>

<AvatarActivity {activity} size={38} rings>
  <AgentAvatar kind={a.avatar?.kind} image={a.avatar?.image} shape={a.avatar?.shape} expression={a.avatar?.expression} color={a.avatar?.color} size={38} live working={!ring && avatarWorking(a.status, st.work)} tool={st.work === "tool" || st.work === "subagent"} toolName={st.work === "tool" ? a.current_action : ""} toolError={a.tool_error} remote={!ring && st.work === "waiting"} events={a.avatar?.events} asleep={a.disabled || dozing} {hatching} alert={st.attention} />
</AvatarActivity>
