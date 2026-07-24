<script lang="ts">
  import { onMount } from 'svelte';
  import {
    consoleApi,
    ConsoleApiError,
    hasPermission,
    type CapabilityAssessment,
    type CapabilityState,
    type FacetState,
    type Overview,
    type RemediationKind,
    type Session
  } from '$lib/console';
  import {
    consoleTranslate,
    facetKindKey,
    facetStateKey,
    reasonKey,
    roleKey,
    stateKey,
    type ConsoleMessageKey,
    type Locale
  } from '$lib/console-i18n';

  type Status = 'loading' | 'anon' | 'ready' | 'forbidden' | 'error';

  let locale = $state<Locale>('en');
  let status = $state<Status>('loading');
  let session = $state<Session | null>(null);
  let overview = $state<Overview | null>(null);
  let selected = $state<CapabilityAssessment | null>(null);
  let selecting = $state('');

  const t = $derived((key: ConsoleMessageKey) => consoleTranslate(locale, key));

  // Non-color state cues: every state also carries a text symbol so status never
  // depends on color alone.
  const stateSymbols: Record<CapabilityState, string> = {
    planned: '◷',
    blocked: '⊘',
    installing: '↻',
    healthy: '✓',
    degraded: '▲',
    failed: '✕',
    disabled: '—'
  };
  const facetSymbols: Record<FacetState, string> = {
    satisfied: '✓',
    pending: '◷',
    progressing: '↻',
    degraded: '▲',
    failed: '✕',
    blocked: '⊘',
    unknown: '?',
    'not-applicable': '—'
  };
  const routeLabels: Record<RemediationKind, ConsoleMessageKey> = {
    'setup-journey': 'routeSetup',
    'git-proposal': 'routeProposal',
    'runtime-action': 'routeRuntimeAction',
    documentation: 'routeDocs',
    grafana: 'routeGrafana',
    argocd: 'routeArgo'
  };

  function formatTime(value: string | undefined): string {
    if (!value) return t('evidenceNever');
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime()) || parsed.getTime() === 0) return t('evidenceNever');
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(parsed);
  }

  async function loadOverview() {
    try {
      overview = await consoleApi.overview();
      status = 'ready';
    } catch (err) {
      if (err instanceof ConsoleApiError && err.status === 401) {
        status = 'anon';
      } else if (err instanceof ConsoleApiError && err.status === 403) {
        status = 'forbidden';
      } else {
        status = 'error';
      }
    }
  }

  async function start() {
    status = 'loading';
    try {
      session = await consoleApi.session();
    } catch {
      status = 'error';
      return;
    }
    if (!session.authenticated) {
      status = 'anon';
      return;
    }
    if (!hasPermission(session, 'observe')) {
      status = 'forbidden';
      return;
    }
    await loadOverview();
  }

  async function selectCapability(id: string) {
    selecting = id;
    try {
      selected = await consoleApi.capability(id);
    } catch {
      selected = null;
      status = 'error';
    } finally {
      selecting = '';
    }
  }

  async function signOut() {
    await consoleApi.logout();
    session = null;
    overview = null;
    selected = null;
    status = 'anon';
  }

  onMount(() => {
    if (typeof navigator !== 'undefined' && navigator.language?.toLowerCase().startsWith('de')) {
      locale = 'de';
    }
    void start();
  });
</script>

<svelte:head><title>{t('consoleTitle')}</title></svelte:head>

<div class="console" lang={locale}>
  <header>
    <div>
      <h1>{t('consoleTitle')}</h1>
      <p class="tagline">{t('consoleTagline')}</p>
    </div>
    <div class="controls">
      <div class="langtoggle" role="group" aria-label={t('languageLabel')}>
        <button type="button" aria-pressed={locale === 'en'} onclick={() => (locale = 'en')}>EN</button>
        <button type="button" aria-pressed={locale === 'de'} onclick={() => (locale = 'de')}>DE</button>
      </div>
      {#if session?.authenticated}
        <span class="whoami">
          {t('signedInAs')} <strong>{session.username || session.subject}</strong>
          · {t('roleLabel')}: {t(roleKey(session.role))}
        </span>
        <button type="button" class="secondary" onclick={signOut}>{t('signOut')}</button>
      {/if}
    </div>
  </header>

  <p class="status" role="status" aria-live="polite">
    {#if status === 'loading'}{t('loading')}{/if}
  </p>

  {#if status === 'anon'}
    <section class="panel">
      <p>{t('signInPrompt')}</p>
      <a class="button" href={consoleApi.loginPath}>{t('signIn')}</a>
    </section>
  {:else if status === 'forbidden'}
    <section class="panel" role="alert">
      <h2>{t('forbidden')}</h2>
      <p>{t('forbiddenDetail')}</p>
    </section>
  {:else if status === 'error'}
    <section class="panel" role="alert">
      <p>{t('loadError')}</p>
      <button type="button" onclick={start}>{t('retry')}</button>
    </section>
  {:else if status === 'ready' && overview}
    {#if selected}
      <section class="panel" aria-labelledby="detail-heading">
        <button type="button" class="link" onclick={() => (selected = null)}>← {t('backToOverview')}</button>
        <h2 id="detail-heading">{selected.capabilityId}</h2>
        <p class="headline state-{selected.state}">
          <span aria-hidden="true" class="sym">{stateSymbols[selected.state]}</span>
          <span class="sr-label">{t('headlineLabel')}:</span>
          <strong>{t(stateKey(selected.state))}</strong>
          — {t(reasonKey(selected.reasonCode))}
        </p>

        <h3>{t('facetsHeading')}</h3>
        <ul class="facets">
          {#each selected.facets as facet}
            <li class="facet fstate-{facet.state}">
              <div class="facet-head">
                <span aria-hidden="true" class="sym">{facetSymbols[facet.state]}</span>
                <strong>{t(facetKindKey(facet.kind))}</strong>
                <span class="badge">{t(facetStateKey(facet.state))}</span>
                {#if facet.stale}<span class="badge warn">{t('evidenceStale')}</span>{/if}
              </div>
              <p class="reason">{t(reasonKey(facet.reasonCode))}</p>
              <p class="meta">{t('evidenceObserved')}: {formatTime(facet.observedAt)}</p>
              {#if facet.remediation}
                <p class="remediation">
                  <span class="badge muted">{t('remediationLabel')}</span>
                  {t(routeLabels[facet.remediation.kind])}
                  {#if facet.remediation.reference}<code>{facet.remediation.reference}</code>{/if}
                </p>
              {/if}
            </li>
          {/each}
        </ul>
      </section>
    {:else}
      <section class="panel" aria-labelledby="overview-heading">
        <h2 id="overview-heading">{t('overviewHeading')}</h2>
        <h3>{t('capabilitiesHeading')}</h3>
        {#if overview.capabilities.length === 0}
          <p>{t('noCapabilities')}</p>
        {:else}
          <p class="hint">{t('selectCapabilityHint')}</p>
          <ul class="capabilities">
            {#each overview.capabilities as capability}
              <li>
                <button
                  type="button"
                  class="capability state-{capability.state}"
                  aria-busy={selecting === capability.id}
                  onclick={() => selectCapability(capability.id)}
                >
                  <span aria-hidden="true" class="sym">{stateSymbols[capability.state]}</span>
                  <span class="name">{capability.id}</span>
                  <span class="badge">{t(stateKey(capability.state))}</span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/if}
  {/if}
</div>

<style>
  .console {
    max-width: 60rem;
    margin: 0 auto;
    padding: 1.5rem 1rem 4rem;
    font-family: system-ui, sans-serif;
    line-height: 1.5;
  }
  header {
    display: flex;
    flex-wrap: wrap;
    gap: 1rem;
    justify-content: space-between;
    align-items: flex-start;
    border-bottom: 1px solid color-mix(in srgb, currentColor 20%, transparent);
    padding-bottom: 1rem;
  }
  h1 {
    font-size: 1.5rem;
    margin: 0;
  }
  .tagline {
    margin: 0.25rem 0 0;
    opacity: 0.75;
  }
  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem 1rem;
    align-items: center;
  }
  .whoami {
    font-size: 0.9rem;
  }
  .langtoggle button {
    padding: 0.2rem 0.5rem;
  }
  .langtoggle button[aria-pressed='true'] {
    font-weight: 700;
    text-decoration: underline;
  }
  .status:empty {
    display: none;
  }
  .panel {
    margin-top: 1.5rem;
    padding: 1.25rem;
    border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
    border-radius: 0.6rem;
  }
  .headline {
    font-size: 1.1rem;
  }
  ul.capabilities,
  ul.facets {
    list-style: none;
    padding: 0;
    margin: 0.75rem 0 0;
    display: grid;
    gap: 0.6rem;
  }
  .capability {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    width: 100%;
    text-align: left;
    padding: 0.75rem 1rem;
    border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
    border-radius: 0.5rem;
    background: transparent;
    color: inherit;
    cursor: pointer;
    font-size: 1rem;
  }
  .capability:hover,
  .capability:focus-visible {
    border-color: currentColor;
  }
  .capability .name {
    flex: 1;
    font-weight: 600;
  }
  .facet {
    padding: 0.75rem 1rem;
    border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
    border-radius: 0.5rem;
  }
  .facet-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .sym {
    display: inline-block;
    min-width: 1.2em;
    text-align: center;
    font-weight: 700;
  }
  .badge {
    font-size: 0.8rem;
    padding: 0.1rem 0.5rem;
    border-radius: 1rem;
    border: 1px solid color-mix(in srgb, currentColor 35%, transparent);
  }
  .badge.warn {
    border-style: dashed;
    font-weight: 700;
  }
  .badge.muted {
    opacity: 0.7;
  }
  .reason {
    margin: 0.4rem 0 0.2rem;
  }
  .meta,
  .remediation {
    margin: 0.2rem 0 0;
    font-size: 0.85rem;
    opacity: 0.85;
  }
  .remediation code {
    font-size: 0.85em;
  }
  .hint {
    opacity: 0.75;
    margin: 0.25rem 0 0;
  }
  .sr-label {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }
  a.button,
  .button {
    display: inline-block;
    margin-top: 0.75rem;
    padding: 0.5rem 1rem;
    border: 1px solid currentColor;
    border-radius: 0.5rem;
    text-decoration: none;
    color: inherit;
    cursor: pointer;
    background: transparent;
  }
  button.link {
    background: none;
    border: none;
    color: inherit;
    cursor: pointer;
    padding: 0;
    text-decoration: underline;
    font-size: 0.9rem;
  }
  button.secondary {
    padding: 0.35rem 0.75rem;
  }
  @media (prefers-reduced-motion: reduce) {
    * {
      animation: none !important;
    }
  }
</style>
