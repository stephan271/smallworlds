<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, initializeSession, type ChangePlan, type ClusterProfile, type SetupJourney, type WorkflowRun } from '$lib/api';
  import { translate, type Locale, type MessageKey } from '$lib/i18n';

  type ActivityEvent = {
    id: number;
    type: string;
    messageKey: string;
    parameters: Record<string, unknown>;
    occurredAt: string;
  };

  let locale: Locale = $state('en');
  let ready = $state(false);
  let error = $state('');
  let profiles: ClusterProfile[] = $state([]);
  let activeProfile: ClusterProfile | null = $state(null);
  let journey: SetupJourney | null = $state(null);
  let plan: ChangePlan | null = $state(null);
  let run: WorkflowRun | null = $state(null);
  let activities: ActivityEvent[] = $state([]);
  let creating = $state(true);
  let editing = $state(false);
  let busy = $state(false);
  let profileName = $state('');
  let profileLanguage: Locale = $state('en');
  let deploymentMode: 'hetzner' | 'local-lan' | 'local-public' = $state('local-lan');
  let eventSource: EventSource | null = null;
  let pollTimer: number | undefined;

  const message = (key: MessageKey) => translate(locale, key);

  $effect(() => {
    document.documentElement.lang = locale;
  });

  onMount(async () => {
    try {
      await initializeSession();
      profiles = await api.listProfiles();
      const remembered = window.localStorage.getItem('smallworlds.activeProfile');
      const selected = profiles.find((profile) => profile.id === remembered) ?? profiles[0];
      if (selected) {
        await selectProfile(selected);
        creating = false;
      }
      ready = true;
    } catch (reason) {
      error = reason instanceof Error ? reason.message : 'request_failed';
      ready = true;
    }
  });

  onDestroy(() => {
    eventSource?.close();
    if (pollTimer) window.clearTimeout(pollTimer);
  });

  async function selectProfile(profile: ClusterProfile): Promise<void> {
    activeProfile = profile;
    locale = profile.language as Locale;
    profileLanguage = locale;
    deploymentMode = profile.deploymentMode;
    profileName = profile.name;
    window.localStorage.setItem('smallworlds.activeProfile', profile.id);
    journey = await api.getJourney(profile.id);
    plan = null;
    activities = [];
    const runID = window.localStorage.getItem(`smallworlds.run.${profile.id}`);
    if (runID) {
      try {
        run = await api.getRun(runID);
        startEventStream(profile.id);
        if (run.state === 'running') schedulePoll(run.id);
      } catch {
        run = null;
        window.localStorage.removeItem(`smallworlds.run.${profile.id}`);
      }
    } else {
      run = null;
    }
  }

  function showCreateProfile(): void {
    creating = true;
    editing = false;
    profileName = '';
    profileLanguage = locale;
    deploymentMode = 'local-lan';
    plan = null;
  }

  function showEditProfile(): void {
    if (!activeProfile) return;
    creating = false;
    editing = true;
    profileName = activeProfile.name;
    profileLanguage = activeProfile.language as Locale;
    deploymentMode = activeProfile.deploymentMode;
  }

  async function saveProfile(): Promise<void> {
    busy = true;
    error = '';
    try {
      const input = { name: profileName, language: profileLanguage, deploymentMode };
      const saved = editing && activeProfile
        ? await api.updateProfile(activeProfile.id, input)
        : await api.createProfile(input);
      const existing = profiles.findIndex((profile) => profile.id === saved.id);
      profiles = existing === -1
        ? [...profiles, saved]
        : profiles.map((profile) => profile.id === saved.id ? saved : profile);
      creating = false;
      editing = false;
      await selectProfile(saved);
    } catch (reason) {
      error = reason instanceof Error ? reason.message : 'request_failed';
    } finally {
      busy = false;
    }
  }

  async function createPlan(): Promise<void> {
    if (!activeProfile) return;
    busy = true;
    error = '';
    try {
      plan = await api.createVerificationPlan(activeProfile.id);
    } catch (reason) {
      error = reason instanceof Error ? reason.message : 'request_failed';
    } finally {
      busy = false;
    }
  }

  async function approvePlan(): Promise<void> {
    if (!plan || !activeProfile) return;
    busy = true;
    error = '';
    try {
      run = await api.approvePlan(plan.id);
      window.localStorage.setItem(`smallworlds.run.${activeProfile.id}`, run.id);
      startEventStream(activeProfile.id);
      schedulePoll(run.id);
    } catch (reason) {
      error = reason instanceof Error ? reason.message : 'request_failed';
    } finally {
      busy = false;
    }
  }

  function schedulePoll(runID: string): void {
    if (pollTimer) window.clearTimeout(pollTimer);
    pollTimer = window.setTimeout(async () => {
      try {
        run = await api.getRun(runID);
        if (run.state === 'running') schedulePoll(runID);
      } catch (reason) {
        error = reason instanceof Error ? reason.message : 'request_failed';
      }
    }, 80);
  }

  function startEventStream(profileID: string): void {
    eventSource?.close();
    eventSource = new EventSource(`/api/v1/events?profileId=${encodeURIComponent(profileID)}&cursor=0`);
    eventSource.addEventListener('workflow', (rawEvent) => {
      const parsed = JSON.parse((rawEvent as MessageEvent<string>).data) as ActivityEvent;
      if (!activities.some((event) => event.id === parsed.id)) activities = [...activities, parsed];
    });
  }

  function runLabel(state: string): string {
    if (state === 'verified') return message('verified');
    if (state === 'cancelled') return message('cancelled');
    if (state === 'failed') return message('failed');
    return message('running');
  }
</script>

<svelte:head>
  <title>{message('product')}</title>
  <meta name="description" content={message('subtitle')} />
</svelte:head>

<header class="product-header">
  <a class="brand" href="/" aria-label={message('product')}>
    <span class="mark" aria-hidden="true">S</span>
    <span class="brand-copy">
      <h1>{message('product')}</h1>
      <small>{message('subtitle')}</small>
    </span>
  </a>
  <label class="locale-control">
    <span>{message('language')}</span>
    <select bind:value={locale} onchange={() => profileLanguage = locale}>
      <option value="en">English</option>
      <option value="de">Deutsch</option>
    </select>
  </label>
</header>

{#if !ready}
  <main class="centered"><p role="status">{message('loading')}</p></main>
{:else}
  <div class="shell">
    <aside aria-label={message('profiles')}>
      <h2>{message('profiles')}</h2>
      <nav>
        {#each profiles as profile (profile.id)}
          <button class:active={activeProfile?.id === profile.id} onclick={() => { creating = false; editing = false; void selectProfile(profile); }}>
            <span>{profile.name}</span>
            <small>{profile.deploymentMode}</small>
          </button>
        {/each}
      </nav>
      <button class="secondary full" onclick={showCreateProfile}>{message('createAnother')}</button>
    </aside>

    <main>
      {#if error}
        <div class="error" role="alert">
          <strong>{message('failed')}</strong>
          <span>{error}</span>
        </div>
      {/if}

      {#if creating || editing}
        <section class="card form-card" aria-labelledby="profile-form-title">
          <p class="eyebrow">{message('profiles')}</p>
          <h1 id="profile-form-title">{editing ? message('editProfile') : message('createTitle')}</h1>
          <form onsubmit={(event) => { event.preventDefault(); void saveProfile(); }}>
            <label>
              <span>{message('profileName')}</span>
              <input bind:value={profileName} required maxlength="120" autocomplete="off" />
            </label>
            <div class="form-grid">
              <label>
                <span>{message('language')}</span>
                <select bind:value={profileLanguage} onchange={() => locale = profileLanguage}>
                  <option value="en">English</option>
                  <option value="de">Deutsch</option>
                </select>
              </label>
              <label>
                <span>{message('deploymentMode')}</span>
                <select bind:value={deploymentMode}>
                  <option value="local-lan">{message('localLan')}</option>
                  <option value="local-public">{message('localPublic')}</option>
                  <option value="hetzner">{message('hetzner')}</option>
                </select>
              </label>
            </div>
            <div class="actions">
              {#if editing}
                <button type="button" class="secondary" onclick={() => editing = false}>{message('cancel')}</button>
              {/if}
              <button type="submit" disabled={busy}>{editing ? message('saveProfile') : message('createProfile')}</button>
            </div>
          </form>
        </section>
      {:else if activeProfile}
        <section class="profile-heading">
          <div>
            <p class="eyebrow">{activeProfile.deploymentMode}</p>
            <h1>{activeProfile.name}</h1>
          </div>
          <button class="secondary" onclick={showEditProfile}>{message('editProfile')}</button>
        </section>

        <div role="status" aria-live="polite" aria-atomic="true" class:verified={run?.state === 'verified'} class="run-status">
          <span class="status-icon" aria-hidden="true">{run?.state === 'verified' ? '✓' : '•'}</span>
          <span>{run ? runLabel(run.state) : message('ready')}</span>
          {#if run}<small>{run.currentCheckpoint}</small>{/if}
        </div>

        <section aria-labelledby="next-title">
          <p class="eyebrow">{message('next')}</p>
          <div class="card task-card">
            <div>
              <h2 id="next-title">{message('task')}</h2>
              <p>{message('taskDescription')}</p>
              {#if journey?.tasks[0]}<span class="badge">{journey.tasks[0].state}</span>{/if}
            </div>
            {#if !plan}
              <button onclick={() => void createPlan()} disabled={busy}>{message('inspectPlan')}</button>
            {/if}
          </div>
        </section>

        {#if plan}
          <section class="card plan-card" aria-labelledby="plan-title">
            <p class="eyebrow">Inspect · Plan · Approve</p>
            <h2 id="plan-title">{message('planTitle')}</h2>
            <dl>
              <div><dt>{message('digest')}</dt><dd data-testid="plan-digest"><code>{plan.digest}</code></dd></div>
              <div><dt>Effect</dt><dd>{message('effect')}</dd></div>
              <div><dt>Risk</dt><dd>{message('noRisk')}</dd></div>
            </dl>
            <div class="actions">
              <button onclick={() => void approvePlan()} disabled={busy || run?.state === 'running'}>{message('approve')}</button>
            </div>
          </section>
        {/if}

        <section aria-labelledby="activity-title">
          <p class="eyebrow">Workflow Run</p>
          <h2 id="activity-title">{message('activity')}</h2>
          {#if activities.length === 0}
            <p class="muted">—</p>
          {:else}
            <ol class="timeline">
              {#each activities as activity (activity.id)}
                <li><span aria-hidden="true"></span><code>{activity.type}</code></li>
              {/each}
            </ol>
          {/if}
        </section>
      {/if}
    </main>
  </div>
{/if}

<style>
  :global(*) { box-sizing: border-box; }
  :global(:root) { font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #17211b; background: #f4f7f2; font-synthesis: none; }
  :global(body) { margin: 0; min-width: 320px; min-height: 100vh; }
  :global(button), :global(input), :global(select) { font: inherit; }
  :global(button) { border: 0; border-radius: .75rem; background: #176b45; color: white; padding: .72rem 1rem; font-weight: 700; cursor: pointer; }
  :global(button:hover) { background: #0f5737; }
  :global(button:focus-visible), :global(input:focus-visible), :global(select:focus-visible), :global(a:focus-visible) { outline: 3px solid #ef9f27; outline-offset: 3px; }
  :global(button:disabled) { opacity: .55; cursor: wait; }
  .product-header { min-height: 5rem; display: flex; align-items: center; justify-content: space-between; padding: 1rem clamp(1rem, 4vw, 3rem); background: #123b2a; color: white; border-bottom: 1px solid #275c46; }
  .brand { color: inherit; text-decoration: none; display: flex; align-items: center; gap: .85rem; }
  .brand-copy { display: grid; gap: .2rem; }
  .brand h1 { margin: 0; font-size: 1rem; letter-spacing: 0; }
  .brand small { color: #bdd6c9; }
  .mark { display: grid; place-items: center; width: 2.7rem; height: 2.7rem; border-radius: .8rem; background: #ef9f27; color: #173325; font-weight: 900; font-size: 1.25rem; }
  .locale-control { display: flex; align-items: center; gap: .6rem; font-size: .9rem; }
  .locale-control select { background: #fff; color: #17211b; }
  .shell { display: grid; grid-template-columns: minmax(14rem, 18rem) minmax(0, 1fr); min-height: calc(100vh - 5rem); }
  aside { padding: 2rem 1.25rem; background: #e6eee8; border-right: 1px solid #cbd9cf; }
  aside h2 { margin-top: 0; font-size: 1rem; text-transform: uppercase; letter-spacing: .08em; color: #54675b; }
  nav { display: grid; gap: .5rem; margin-bottom: 1rem; }
  nav button { display: grid; text-align: left; gap: .2rem; color: #233c2e; background: transparent; border: 1px solid transparent; }
  nav button:hover, nav button.active { background: white; border-color: #b9cbbf; }
  nav button small { font-weight: 500; color: #46564c; }
  main { width: min(58rem, 100%); padding: clamp(1.5rem, 5vw, 4rem); }
  .centered { margin: 4rem auto; }
  h1 { margin: .2rem 0; font-size: clamp(2rem, 5vw, 3.5rem); letter-spacing: -.04em; }
  h2 { margin: .2rem 0 .7rem; }
  .eyebrow { margin: 0 0 .35rem; color: #5b6e61; font-size: .78rem; font-weight: 800; text-transform: uppercase; letter-spacing: .12em; }
  .profile-heading { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 1.5rem; }
  .card { border-radius: 1rem; background: white; border: 1px solid #d5ded7; box-shadow: 0 10px 30px rgba(26, 55, 38, .06); padding: clamp(1.25rem, 3vw, 2rem); }
  .form-card { max-width: 42rem; }
  form, form label { display: grid; gap: .5rem; }
  form { gap: 1.25rem; }
  form label span { font-weight: 750; }
  input, select { width: 100%; min-height: 2.8rem; border: 1px solid #9eb0a4; border-radius: .65rem; padding: .65rem .75rem; background: white; color: #17211b; }
  .form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
  .actions { display: flex; justify-content: flex-end; gap: .7rem; }
  button.secondary { background: transparent; border: 1px solid #8ca092; color: #244932; }
  button.secondary:hover { background: #eef3ef; }
  button.full { width: 100%; }
  .task-card { display: flex; align-items: center; justify-content: space-between; gap: 1.5rem; margin-bottom: 2rem; border-left: 5px solid #ef9f27; }
  .task-card p { max-width: 45rem; color: #53645a; }
  .badge { display: inline-flex; border-radius: 2rem; background: #e6f1e9; color: #176b45; padding: .25rem .6rem; font-size: .8rem; font-weight: 800; }
  .run-status { display: flex; align-items: center; gap: .65rem; min-height: 3rem; margin: 1rem 0 2rem; padding: .65rem 1rem; border-radius: .8rem; background: #e8ede9; font-weight: 800; }
  .run-status small { margin-left: auto; color: #68766d; font-weight: 600; }
  .run-status.verified { background: #daf1e1; color: #145f3d; }
  .status-icon { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border-radius: 50%; background: currentColor; color: white; }
  .verified .status-icon { background: #176b45; }
  .plan-card { margin: 0 0 2rem; }
  dl { display: grid; gap: .8rem; }
  dl div { display: grid; grid-template-columns: minmax(8rem, 11rem) 1fr; gap: 1rem; border-top: 1px solid #e0e6e1; padding-top: .8rem; }
  dt { color: #617066; font-weight: 700; }
  dd { margin: 0; overflow-wrap: anywhere; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em; }
  .timeline { list-style: none; padding: 0; display: grid; gap: .65rem; }
  .timeline li { display: flex; align-items: center; gap: .7rem; }
  .timeline li span { width: .7rem; height: .7rem; border-radius: 50%; background: #176b45; }
  .muted { color: #718077; }
  .error { display: flex; gap: 1rem; padding: 1rem; margin-bottom: 1rem; border: 1px solid #b5473b; border-radius: .8rem; background: #fff1ee; color: #78281f; }
  @media (max-width: 760px) {
    .product-header { align-items: flex-start; }
    .brand small, .locale-control span { display: none; }
    .shell { grid-template-columns: 1fr; }
    aside { border-right: 0; border-bottom: 1px solid #cbd9cf; padding: 1rem; }
    nav { grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr)); }
    main { padding: 1.25rem; }
    .form-grid, dl div { grid-template-columns: 1fr; }
    .task-card, .profile-heading { align-items: stretch; flex-direction: column; }
  }
  @media (prefers-reduced-motion: reduce) { :global(*) { scroll-behavior: auto !important; transition: none !important; animation: none !important; } }
  @media (prefers-contrast: more) { :global(:root) { background: white; color: black; } .card, input, select, button.secondary { border-width: 2px; border-color: currentColor; } }
</style>
