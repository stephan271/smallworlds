<script lang="ts">
  import { onMount } from 'svelte';
  import {
    consoleApi,
    ConsoleApiError,
    hasPermission,
    type AddCapabilityOffer,
    type AdminAccess,
    type CapabilityAssessment,
    type CapabilityState,
    type DatasetProtection,
    type FacetState,
    type InvitationResponse,
    type OperatorDevice,
    type Overview,
    type PlanResponse,
    type ProposeResponse,
    type ProtectionLevel,
    type AvailableRelease,
    type ClusterProfile,
    type ReleaseAdoption,
    type ReleasePlanResponse,
    type ReleaseProposalResponse,
    type RemediationKind,
    type RevocationExecuteResponse,
    type RevocationPlanResponse,
    type Session
  } from '$lib/console';
  import {
    catalogLabelKey,
    consoleTranslate,
    dataTypeKey,
    facetKindKey,
    facetStateKey,
    lockoutKey,
    protectionLevelKey,
    reasonKey,
    roleKey,
    stateKey,
    stepKey,
    type ConsoleMessageKey,
    type Locale
  } from '$lib/console-i18n';

  type Status = 'loading' | 'anon' | 'ready' | 'forbidden' | 'error';
  type View = 'capabilities' | 'protection' | 'additions' | 'updates' | 'access';
  type AdditionStage = 'offers' | 'plan' | 'proposed';
  type UpdateStage = 'available' | 'plan' | 'proposed';
  type RevokeStage = 'idle' | 'planned' | 'done';

  let locale = $state<Locale>('en');
  let status = $state<Status>('loading');
  let view = $state<View>('capabilities');
  let session = $state<Session | null>(null);
  let overview = $state<Overview | null>(null);
  let selected = $state<CapabilityAssessment | null>(null);
  let selecting = $state('');
  let protection = $state<DatasetProtection[] | null>(null);
  let protectionError = $state(false);

  // Add-capability journey state.
  let offers = $state<AddCapabilityOffer[] | null>(null);
  let offersError = $state(false);
  let additionStage = $state<AdditionStage>('offers');
  let planning = $state('');
  let currentPlan = $state<PlanResponse | null>(null);
  let approving = $state(false);
  let proposing = $state(false);
  let approved = $state(false);
  let proposal = $state<ProposeResponse | null>(null);
  let additionError = $state<ConsoleMessageKey | null>(null);

  // Explicit release-update journey state.
  let clusterProfile = $state<ClusterProfile | null>(null);
  let availableRelease = $state<AvailableRelease | null>(null);
  let updateStage = $state<UpdateStage>('available');
  let updatePlan = $state<ReleasePlanResponse | null>(null);
  let updateApproved = $state(false);
  let updateProposal = $state<ReleaseProposalResponse | null>(null);
  let updateAdoption = $state<ReleaseAdoption | null>(null);
  let updateLoading = $state(false);
  let updateError = $state<ConsoleMessageKey | null>(null);

  // Device-access (Administer) journey state.
  let access = $state<AdminAccess | null>(null);
  let accessError = $state<ConsoleMessageKey | null>(null);
  let inviteLabel = $state('');
  let inviting = $state(false);
  let invitation = $state<InvitationResponse | null>(null);
  let revokeStage = $state<RevokeStage>('idle');
  let revokeTarget = $state<OperatorDevice | null>(null);
  let revokePlan = $state<RevocationPlanResponse | null>(null);
  let revokeApproved = $state(false);
  let revoking = $state(false);
  let approvingRevoke = $state(false);
  let revokeResult = $state<RevocationExecuteResponse | null>(null);

  const t = $derived((key: ConsoleMessageKey) => consoleTranslate(locale, key));
  const planFits = $derived(
    currentPlan
      ? currentPlan.plan.resources.fitsMemory && currentPlan.plan.resources.fitsStorage
      : false
  );

  function catLabel(value: string): string {
    const key = catalogLabelKey(value);
    return key ? t(key) : value;
  }

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
  const protectionSymbols: Record<ProtectionLevel, string> = {
    unknown: '?',
    none: '✕',
    'local-only': '△',
    stale: '▲',
    protected: '✓'
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

  async function showProtection() {
    view = 'protection';
    if (protection !== null) return;
    try {
      protection = (await consoleApi.protection()).datasets;
      protectionError = false;
    } catch {
      protection = [];
      protectionError = true;
    }
  }

  async function showAdditions() {
    view = 'additions';
    if (offers !== null) return;
    await loadOffers();
  }

  async function loadOffers() {
    try {
      offers = (await consoleApi.additionOffers()).offers;
      offersError = false;
    } catch {
      offers = [];
      offersError = true;
    }
  }

  function additionErrorFor(err: unknown): ConsoleMessageKey {
    if (err instanceof ConsoleApiError) {
      switch (err.code) {
        case 'capacity_unavailable':
          return 'additionErrorCapacity';
        case 'proposal_unavailable':
          return 'additionErrorProposal';
        case 'capability_not_offered':
          return 'additionErrorNotOffered';
        case 'addition_plan_mismatch':
          return 'additionErrorMismatch';
      }
    }
    return 'additionErrorGeneric';
  }

  async function planAddition(id: string) {
    planning = id;
    additionError = null;
    try {
      currentPlan = await consoleApi.planAddition(id);
      approved = false;
      proposal = null;
      additionStage = 'plan';
    } catch (err) {
      additionError = additionErrorFor(err);
    } finally {
      planning = '';
    }
  }

  async function approvePlan() {
    if (!currentPlan) return;
    approving = true;
    additionError = null;
    try {
      await consoleApi.approveAddition(currentPlan.planId);
      approved = true;
    } catch (err) {
      additionError = additionErrorFor(err);
    } finally {
      approving = false;
    }
  }

  async function proposePlan() {
    if (!currentPlan) return;
    proposing = true;
    additionError = null;
    try {
      proposal = await consoleApi.proposeAddition(currentPlan.planId);
      additionStage = 'proposed';
    } catch (err) {
      additionError = additionErrorFor(err);
    } finally {
      proposing = false;
    }
  }

  function resetAdditions() {
    additionStage = 'offers';
    currentPlan = null;
    approved = false;
    proposal = null;
    additionError = null;
    offers = null; // reload so a just-proposed app drops off the offer list
    void loadOffers();
  }

  function updateErrorFor(err: unknown): ConsoleMessageKey {
    if (err instanceof ConsoleApiError) {
      switch (err.code) {
        case 'release_incompatible':
          return 'updateErrorIncompatible';
        case 'update_plan_mismatch':
        case 'release_metadata_changed':
          return 'updateErrorMismatch';
        case 'proposal_unavailable':
          return 'additionErrorProposal';
      }
    }
    return 'updateErrorGeneric';
  }

  async function showUpdates() {
    view = 'updates';
    if (clusterProfile !== null) return;
    updateLoading = true;
    updateError = null;
    try {
      [clusterProfile, { available: availableRelease }] = await Promise.all([
        consoleApi.clusterProfile(),
        consoleApi.availableRelease()
      ]);
    } catch (err) {
      updateError = updateErrorFor(err);
    } finally {
      updateLoading = false;
    }
  }

  async function planUpdate() {
    if (!availableRelease) return;
    updateLoading = true;
    updateError = null;
    try {
      updatePlan = await consoleApi.planRelease(availableRelease.metadata.release);
      updateApproved = false;
      updateStage = 'plan';
    } catch (err) {
      updateError = updateErrorFor(err);
    } finally {
      updateLoading = false;
    }
  }

  async function approveUpdate() {
    if (!updatePlan) return;
    updateLoading = true;
    updateError = null;
    try {
      await consoleApi.approveRelease(updatePlan.planId);
      updateApproved = true;
    } catch (err) {
      updateError = updateErrorFor(err);
    } finally {
      updateLoading = false;
    }
  }

  async function proposeUpdate() {
    if (!updatePlan) return;
    updateLoading = true;
    updateError = null;
    try {
      updateProposal = await consoleApi.proposeRelease(updatePlan.planId);
      updateStage = 'proposed';
    } catch (err) {
      updateError = updateErrorFor(err);
    } finally {
      updateLoading = false;
    }
  }

  async function refreshAdoption() {
    const release = updatePlan?.plan.toBaseTag ?? availableRelease?.metadata.release;
    if (!release) return;
    updateLoading = true;
    updateError = null;
    try {
      updateAdoption = await consoleApi.releaseAdoption(release);
    } catch {
      updateAdoption = null;
      updateError = 'updateAdoptionUnavailable';
    } finally {
      updateLoading = false;
    }
  }

  function deviceErrorFor(err: unknown): ConsoleMessageKey {
    if (err instanceof ConsoleApiError) {
      switch (err.code) {
        case 'directory_unavailable':
          return 'deviceErrorDirectory';
        case 'invitation_unavailable':
          return 'deviceErrorInvitation';
        case 'revocation_unavailable':
          return 'deviceErrorRevocation';
        case 'revocation_plan_mismatch':
          return 'deviceErrorMismatch';
        case 'revocation_not_approved':
          return 'deviceErrorNotApproved';
        case 'device_not_found':
          return 'deviceErrorNotFound';
      }
    }
    return 'deviceErrorGeneric';
  }

  async function showAccess() {
    view = 'access';
    if (access !== null) return;
    await loadAccess();
  }

  async function loadAccess() {
    accessError = null;
    try {
      access = await consoleApi.administrationAccess();
    } catch (err) {
      access = { devices: [], summary: { totalDevices: 0, ownerDevices: 0 }, activity: [] };
      accessError = deviceErrorFor(err);
    }
  }

  async function createInvitation() {
    if (!inviteLabel.trim()) return;
    inviting = true;
    accessError = null;
    try {
      invitation = await consoleApi.createInvitation(inviteLabel.trim());
      inviteLabel = '';
    } catch (err) {
      accessError = deviceErrorFor(err);
    } finally {
      inviting = false;
    }
  }

  function resetInvitation() {
    invitation = null;
    void loadAccess();
  }

  async function planRevoke(device: OperatorDevice) {
    revokeTarget = device;
    revokeApproved = false;
    revokeResult = null;
    accessError = null;
    revoking = true;
    try {
      revokePlan = await consoleApi.planRevocation(device.stableId);
      revokeStage = 'planned';
    } catch (err) {
      accessError = deviceErrorFor(err);
    } finally {
      revoking = false;
    }
  }

  async function approveRevoke() {
    if (!revokePlan) return;
    approvingRevoke = true;
    accessError = null;
    try {
      await consoleApi.approveRevocation(revokePlan.planId);
      revokeApproved = true;
    } catch (err) {
      accessError = deviceErrorFor(err);
    } finally {
      approvingRevoke = false;
    }
  }

  async function executeRevoke() {
    if (!revokePlan) return;
    revoking = true;
    accessError = null;
    try {
      revokeResult = await consoleApi.executeRevocation(revokePlan.planId);
      revokeStage = 'done';
    } catch (err) {
      accessError = deviceErrorFor(err);
    } finally {
      revoking = false;
    }
  }

  function resetRevoke() {
    revokeStage = 'idle';
    revokeTarget = null;
    revokePlan = null;
    revokeApproved = false;
    revokeResult = null;
    accessError = null;
    void loadAccess();
  }

  async function signOut() {
    await consoleApi.logout();
    session = null;
    overview = null;
    selected = null;
    offers = null;
    additionStage = 'offers';
    currentPlan = null;
    proposal = null;
    clusterProfile = null;
    availableRelease = null;
    updateStage = 'available';
    updatePlan = null;
    updateProposal = null;
    updateAdoption = null;
    access = null;
    invitation = null;
    revokeStage = 'idle';
    revokeTarget = null;
    revokePlan = null;
    revokeApproved = false;
    revokeResult = null;
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
    <nav class="viewnav" aria-label={t('capabilitiesHeading')}>
      <button type="button" aria-pressed={view === 'capabilities'} onclick={() => (view = 'capabilities')}>
        {t('navCapabilities')}
      </button>
      <button type="button" aria-pressed={view === 'protection'} onclick={showProtection}>
        {t('navProtection')}
      </button>
      <button type="button" aria-pressed={view === 'updates'} onclick={showUpdates}>
        {t('navUpdates')}
      </button>
      {#if hasPermission(session, 'propose')}
        <button type="button" aria-pressed={view === 'additions'} onclick={showAdditions}>
          {t('navAdditions')}
        </button>
      {/if}
      {#if hasPermission(session, 'administer')}
        <button type="button" aria-pressed={view === 'access'} onclick={showAccess}>
          {t('navAccess')}
        </button>
      {/if}
    </nav>
    {#if view === 'protection'}
      <section class="panel" aria-labelledby="protection-heading">
        <h2 id="protection-heading">{t('protectionHeading')}</h2>
        <p class="hint">{t('protectionIntro')}</p>
        <p class="badge muted roadmap">{t('protectionRoadmap')}</p>
        {#if protection === null}
          <p>{t('loading')}</p>
        {:else if protectionError}
          <p role="alert">{t('loadError')}</p>
        {:else if protection.length === 0}
          <p>{t('noCapabilities')}</p>
        {:else}
          <ul class="datasets">
            {#each protection as item}
              <li class="dataset plevel-{item.level}">
                <div class="facet-head">
                  <span aria-hidden="true" class="sym">{protectionSymbols[item.level]}</span>
                  <strong>{item.dataset.id}</strong>
                  <span class="badge">{t(protectionLevelKey(item.level))}</span>
                  <span class="badge {item.disasterProtected ? '' : 'warn'}">
                    {item.disasterProtected ? t('disasterProtectedYes') : t('disasterProtectedNo')}
                  </span>
                </div>
                {#if item.level === 'local-only'}
                  <p class="reason">{t('localOnlyWarning')}</p>
                {/if}
                {#if !item.observed}
                  <p class="meta">{t('notObserved')}</p>
                {/if}
                <dl class="evidence">
                  <div><dt>{t('colOwner')}</dt><dd>{item.dataset.capability}</dd></div>
                  <div><dt>{t('colType')}</dt><dd>{t(dataTypeKey(item.dataset.dataType))}</dd></div>
                  <div><dt>{t('colProducer')}</dt><dd>{item.dataset.producer}</dd></div>
                  <div><dt>{t('colSchedule')}</dt><dd>{item.dataset.schedule}</dd></div>
                  <div><dt>{t('colRetention')}</dt><dd>{item.dataset.retention}</dd></div>
                  <div>
                    <dt>{t('colJob')}</dt>
                    <dd>
                      {#if item.jobFailed}{t('jobFailedLabel')}
                      {:else if item.jobCompletedAt}{formatTime(item.jobCompletedAt)}
                      {:else}{t('jobNever')}{/if}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('colLocalRP')}</dt>
                    <dd>
                      {#if item.localRecoveryPointAt}{formatTime(item.localRecoveryPointAt)}{#if item.localRecoveryPointStale} {t('staleSuffix')}{/if}
                      {:else}{t('noRecoveryPoint')}{/if}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('colOffsiteRP')}</dt>
                    <dd>
                      {#if item.offsiteConfigured && item.offsiteRecoveryPointAt}{formatTime(item.offsiteRecoveryPointAt)}{#if item.offsiteRecoveryPointStale} {t('staleSuffix')}{/if}
                      {:else}{t('noRecoveryPoint')}{/if}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('colRestoreDrill')}</dt>
                    <dd>
                      {#if item.restoreDrillAt}{formatTime(item.restoreDrillAt)} — {item.restoreDrillPassed ? t('restoreDrillPassed') : t('restoreDrillFailed')}
                      {:else}{t('restoreDrillNone')}{/if}
                    </dd>
                  </div>
                </dl>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {:else if view === 'additions'}
      <section class="panel" aria-labelledby="additions-heading">
        <h2 id="additions-heading">{t('additionsHeading')}</h2>
        <p class="hint">{t('additionsIntro')}</p>
        {#if additionError}
          <p class="badge warn errorline" role="alert">{t(additionError)}</p>
        {/if}

        {#if additionStage === 'offers'}
          {#if offers === null}
            <p>{t('loading')}</p>
          {:else if offersError}
            <p role="alert">{t('loadError')}</p>
          {:else if offers.length === 0}
            <p>{t('additionsEmpty')}</p>
          {:else}
            <ul class="capabilities">
              {#each offers as offer}
                <li class="offer">
                  <div class="offer-head">
                    <span class="name">{offer.id}</span>
                    <span class="badge">{catLabel(offer.exposure)}</span>
                    {#if offer.stateful}<span class="badge">{t('offerStores')}</span>{/if}
                    <span class="badge muted">{offer.resources.memoryMi} Mi · {offer.resources.storageGi} Gi</span>
                  </div>
                  {#if offer.disabledDependencies && offer.disabledDependencies.length > 0}
                    <p class="meta">{t('offerAlsoEnables')}: {offer.disabledDependencies.join(', ')}</p>
                  {/if}
                  <button
                    type="button"
                    class="button"
                    aria-busy={planning === offer.id}
                    onclick={() => planAddition(offer.id)}
                  >
                    {t('addPlanButton')}
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        {:else if additionStage === 'plan' && currentPlan}
          <button type="button" class="link" onclick={resetAdditions}>← {t('planBackToOffers')}</button>
          <h3>{t('planHeading')}: {currentPlan.plan.target}</h3>
          <p><strong>{t('planAddsLabel')}:</strong> {currentPlan.plan.addedCapabilities.join(', ')}</p>
          {#if currentPlan.plan.presentDependencies && currentPlan.plan.presentDependencies.length > 0}
            <p class="meta">{t('planPresentLabel')}: {currentPlan.plan.presentDependencies.join(', ')}</p>
          {/if}

          <h4>{t('planResourcesHeading')}</h4>
          <dl class="evidence">
            <div><dt>{t('planMemory')} — {t('planNeeded')}</dt><dd>{currentPlan.plan.resources.requiredMemoryMi} Mi</dd></div>
            <div><dt>{t('planMemory')} — {t('planAvailable')}</dt><dd>{currentPlan.plan.resources.availableMemoryMi} Mi</dd></div>
            <div><dt>{t('planStorage')} — {t('planNeeded')}</dt><dd>{currentPlan.plan.resources.requiredStorageGi} Gi</dd></div>
            <div><dt>{t('planStorage')} — {t('planAvailable')}</dt><dd>{currentPlan.plan.resources.availableStorageGi} Gi</dd></div>
          </dl>
          <p class="badge {planFits ? '' : 'warn'} fitline">
            <span aria-hidden="true" class="sym">{planFits ? '✓' : '▲'}</span>
            {planFits ? t('planFitsYes') : t('planFitsNo')}
          </p>

          <dl class="evidence">
            <div><dt>{t('planExposureLabel')}</dt><dd>{(currentPlan.plan.exposure ?? []).map(catLabel).join(', ')}</dd></div>
            <div><dt>{t('planProtectionLabel')}</dt><dd>{(currentPlan.plan.protection ?? []).map(catLabel).join(', ')}</dd></div>
            <div>
              <dt>{t('planDataLabel')}</dt>
              <dd>
                {#if currentPlan.plan.persistentData && currentPlan.plan.persistentData.length > 0}
                  {currentPlan.plan.persistentData.join(', ')}
                {:else}{t('planDataNone')}{/if}
              </dd>
            </div>
          </dl>

          <h4>{t('planDiffHeading')}</h4>
          <p class="hint">{t('planDiffHint')}</p>
          <pre class="diff"><code>{currentPlan.plan.gitDiff}</code></pre>

          <div class="actions">
            <button type="button" class="button" disabled={approved} aria-busy={approving} onclick={approvePlan}>
              {#if approved}✓ {/if}{t('approveButton')}
            </button>
            <button type="button" class="button" disabled={!approved} aria-busy={proposing} onclick={proposePlan}>
              {t('proposeButton')}
            </button>
          </div>
        {:else if additionStage === 'proposed' && proposal}
          <h3>{t('proposalOpenedHeading')}</h3>
          <dl class="evidence">
            <div><dt>{t('proposalProvider')}</dt><dd>{proposal.provider}</dd></div>
            {#if proposal.branch}<div><dt>{t('proposalBranch')}</dt><dd>{proposal.branch}</dd></div>{/if}
            <div><dt>{t('proposalCommit')}</dt><dd><code>{proposal.commit}</code></dd></div>
          </dl>
          {#if proposal.url}
            <p>
              <a class="button" href={proposal.url} target="_blank" rel="noopener noreferrer">
                {t('proposalOpenLink')} <span class="sr-label">({t('opensNewTab')})</span>
              </a>
            </p>
          {/if}
          <p class="reason">{t('proposalMergeObserved')}</p>
          <button type="button" class="button" onclick={resetAdditions}>{t('addAnother')}</button>
        {/if}
      </section>
    {:else if view === 'updates'}
      <section class="panel" aria-labelledby="updates-heading">
        <h2 id="updates-heading">{t('updatesHeading')}</h2>
        <p class="hint">{t('updatesIntro')}</p>
        {#if updateError}
          <p class="badge warn errorline" role="alert">{t(updateError)}</p>
        {/if}
        {#if updateLoading && !clusterProfile}
          <p>{t('loading')}</p>
        {:else if clusterProfile}
          <h3>{t('updateProfileHeading')}</h3>
          <dl class="evidence">
            <div><dt>{t('updateCurrentRelease')}</dt><dd><code>{clusterProfile.baseTag}</code></dd></div>
            <div><dt>{t('updateLauncherVersion')}</dt><dd><code>{clusterProfile.launcherVersion}</code></dd></div>
            <div><dt>{t('updateClusterVersion')}</dt><dd><code>{clusterProfile.clusterVersion}</code></dd></div>
            <div><dt>{t('updateCatalogVersion')}</dt><dd>{clusterProfile.catalogVersion}</dd></div>
          </dl>
          <p>
            <a class="button" href="/api/v1/updates/profile/export" download="smallworlds-cluster-profile.json">
              {t('updateExportProfile')}
            </a>
          </p>

          {#if updateStage === 'available'}
            {#if !availableRelease}
              <p>{t('updateNoRelease')}</p>
            {:else}
              <h3>{t('updateAvailableHeading')}: {availableRelease.metadata.release}</h3>
              <p class="badge">
                <span aria-hidden="true" class="sym">✓</span>{t('updateSignatureValid')}
              </p>
              <p class="badge {availableRelease.compatibility.compatible ? '' : 'warn'} fitline">
                <span aria-hidden="true" class="sym">{availableRelease.compatibility.compatible ? '✓' : '▲'}</span>
                {availableRelease.compatibility.compatible ? t('updateCompatibilityYes') : t('updateCompatibilityNo')}
              </p>
              {#if !availableRelease.compatibility.compatible}
                <ul>
                  {#each availableRelease.compatibility.reasons as reason}<li><code>{reason}</code></li>{/each}
                </ul>
              {/if}
              <dl class="evidence">
                <div><dt>{t('updateCurrentRelease')}</dt><dd><code>{availableRelease.metadata.baseTag}</code></dd></div>
                <div><dt>{t('updateCatalogVersion')}</dt><dd>{availableRelease.metadata.catalogVersion}</dd></div>
              </dl>
              <h4>{t('updateNotesHeading')}</h4>
              <ul>{#each availableRelease.metadata.releaseNotes as note}<li>{note}</li>{/each}</ul>
              {#if availableRelease.metadata.capabilityChanges.length > 0}
                <h4>{t('updateCapabilitiesHeading')}</h4>
                <ul>
                  {#each availableRelease.metadata.capabilityChanges as change}
                    <li><strong>{change.id}</strong> — {change.change}: {change.detail}</li>
                  {/each}
                </ul>
              {/if}
              {#if hasPermission(session, 'propose')}
                <button
                  type="button"
                  class="button"
                  disabled={!availableRelease.compatibility.compatible}
                  aria-busy={updateLoading}
                  onclick={planUpdate}
                >
                  {t('updatePlanButton')}
                </button>
              {/if}
            {/if}
          {:else if updateStage === 'plan' && updatePlan}
            <h3>{t('planHeading')}: {updatePlan.plan.fromBaseTag} → {updatePlan.plan.toBaseTag}</h3>
            <h4>{t('updateNotesHeading')}</h4>
            <ul>{#each updatePlan.plan.releaseNotes as note}<li>{note}</li>{/each}</ul>

            <h4>{t('updateCapabilitiesHeading')}</h4>
            {#if updatePlan.plan.capabilityChanges.length === 0}
              <p>{t('noCapabilities')}</p>
            {:else}
              <ul>
                {#each updatePlan.plan.capabilityChanges as change}
                  <li><strong>{change.id}</strong> — {change.change}: {change.detail}</li>
                {/each}
              </ul>
            {/if}

            <h4>{t('updatePinsHeading')}</h4>
            <dl class="evidence pins">
              {#each Object.entries(updatePlan.plan.images) as [name, pin]}
                <div><dt>image/{name}</dt><dd><code>{pin}</code></dd></div>
              {/each}
              {#each Object.entries(updatePlan.plan.tools) as [name, pin]}
                <div><dt>tool/{name}</dt><dd><code>{pin}</code></dd></div>
              {/each}
            </dl>

            <h4>{t('updateRisksHeading')}</h4>
            <dl class="evidence">
              <div><dt>{t('updateDowntimeRisks')}</dt><dd>{updatePlan.plan.risks.downtime.join(' · ')}</dd></div>
              <div><dt>{t('updateDataRisks')}</dt><dd>{updatePlan.plan.risks.data.join(' · ')}</dd></div>
              <div><dt>{t('updateExposureRisks')}</dt><dd>{updatePlan.plan.risks.exposure.join(' · ')}</dd></div>
            </dl>
            <h4>{t('updateRecoveryHeading')}</h4>
            <p>{updatePlan.plan.recovery.expected}</p>
            <ol>{#each updatePlan.plan.recovery.steps as step}<li>{step}</li>{/each}</ol>

            <h4>{t('planDiffHeading')}</h4>
            <pre class="diff"><code>{updatePlan.plan.gitDiff}</code></pre>
            <p class="reason">{t('updateProposalSafety')}</p>
            <div class="actions">
              <button type="button" class="button" disabled={updateApproved} aria-busy={updateLoading} onclick={approveUpdate}>
                {#if updateApproved}✓ {/if}{t('approveButton')}
              </button>
              <button type="button" class="button" disabled={!updateApproved} aria-busy={updateLoading} onclick={proposeUpdate}>
                {t('proposeButton')}
              </button>
            </div>
          {:else if updateStage === 'proposed' && updateProposal}
            <h3>{t('updateProposalOpened')}</h3>
            <dl class="evidence">
              <div><dt>{t('proposalProvider')}</dt><dd>{updateProposal.provider}</dd></div>
              {#if updateProposal.branch}<div><dt>{t('proposalBranch')}</dt><dd>{updateProposal.branch}</dd></div>{/if}
              <div><dt>{t('proposalCommit')}</dt><dd><code>{updateProposal.commit}</code></dd></div>
            </dl>
            {#if updateProposal.url}
              <p><a class="button" href={updateProposal.url} target="_blank" rel="noopener noreferrer">{t('proposalOpenLink')}</a></p>
            {/if}
            <p class="reason">{t('updateProposalSafety')}</p>
            <h4>{t('updateAdoptionHeading')}</h4>
            <button type="button" class="button" aria-busy={updateLoading} onclick={refreshAdoption}>
              {t('updateRefreshAdoption')}
            </button>
            {#if updateAdoption}
              <p class="badge {updateAdoption.state === 'failed' || updateAdoption.state === 'partial' ? 'warn' : ''} fitline">
                <strong>{updateAdoption.state}</strong>
              </p>
              <dl class="evidence">
                <div>
                  <dt>{t('updateArgoEvidence')}</dt>
                  <dd>{updateAdoption.argoSynced ? 'Synced' : 'OutOfSync'} · {updateAdoption.argoHealthy ? 'Healthy' : 'Not healthy'}</dd>
                </div>
                {#if updateAdoption.argoRevision}<div><dt>Revision</dt><dd><code>{updateAdoption.argoRevision}</code></dd></div>{/if}
              </dl>
              {#if updateAdoption.reasons.length > 0}
                <ul>{#each updateAdoption.reasons as reason}<li><code>{reason}</code></li>{/each}</ul>
              {/if}
            {/if}
          {/if}
        {/if}
      </section>
    {:else if view === 'access'}
      <section class="panel" aria-labelledby="access-heading">
        <h2 id="access-heading">{t('accessHeading')}</h2>
        <p class="hint">{t('accessIntro')}</p>
        {#if accessError}
          <p class="badge warn errorline" role="alert">{t(accessError)}</p>
        {/if}

        {#if access === null}
          <p>{t('loading')}</p>
        {:else if revokeStage === 'planned' && revokePlan}
          <h3>{t('revokeAssessmentHeading')}</h3>
          <dl class="evidence">
            <div><dt>{t('revokeAffected')}</dt><dd>{revokePlan.assessment.target.hostname} <code>{revokePlan.assessment.affectedStableId}</code></dd></div>
            <div><dt>{t('revokeRemaining')}</dt><dd>{revokePlan.assessment.remainingOwnerDevices}</dd></div>
          </dl>
          <p class="badge {revokePlan.assessment.alternativeOwnerAccess ? '' : 'warn'} fitline">
            <span aria-hidden="true" class="sym">{revokePlan.assessment.alternativeOwnerAccess ? '✓' : '▲'}</span>
            {revokePlan.assessment.alternativeOwnerAccess ? t('revokeAlternativeYes') : t('revokeAlternativeNo')}
          </p>
          {#if revokePlan.assessment.lockoutRisk && revokePlan.assessment.lockoutReason}
            <p class="badge warn errorline" role="alert">
              <span aria-hidden="true" class="sym">▲</span>
              <strong>{t('revokeLockoutWarning')}:</strong> {t(lockoutKey(revokePlan.assessment.lockoutReason))}
            </p>
          {/if}
          <div class="actions">
            <button type="button" class="button" disabled={revokeApproved} aria-busy={approvingRevoke} onclick={approveRevoke}>
              {#if revokeApproved}✓ {/if}{t('revokeApproveButton')}
            </button>
            <button type="button" class="button" disabled={!revokeApproved} aria-busy={revoking} onclick={executeRevoke}>
              {t('revokeExecuteButton')}
            </button>
            <button type="button" class="link" onclick={resetRevoke}>← {t('revokeAnother')}</button>
          </div>
        {:else if revokeStage === 'done' && revokeResult}
          <h3>{t('revokeDoneHeading')}</h3>
          <p class="badge {revokeResult.accessVerified ? '' : 'warn'} fitline">
            <span aria-hidden="true" class="sym">{revokeResult.accessVerified ? '✓' : '▲'}</span>
            {revokeResult.accessVerified ? t('revokeAccessVerified') : t('revokeAccessNotVerified')}
          </p>
          <dl class="evidence">
            <div><dt>{t('revokeAffected')}</dt><dd><code>{revokeResult.affectedStableId}</code></dd></div>
          </dl>
          <button type="button" class="button" onclick={resetRevoke}>{t('revokeAnother')}</button>
        {:else}
          <h3>{t('accessDevicesHeading')}</h3>
          <p class="meta">{access.summary.totalDevices} {t('accessSummary')} · {access.summary.ownerDevices} {t('accessOwnerDevices')}</p>
          {#if access.devices.length === 0}
            <p>{t('accessNoDevices')}</p>
          {:else}
            <ul class="datasets">
              {#each access.devices as device}
                <li class="dataset">
                  <div class="facet-head">
                    <span aria-hidden="true" class="sym">{device.online ? '●' : '○'}</span>
                    <strong>{device.hostname}</strong>
                    {#if device.ownerAccess}<span class="badge">{t('deviceOwnerBadge')}</span>{/if}
                    {#if device.self}<span class="badge muted">{t('deviceSelfBadge')}</span>{/if}
                    <span class="badge muted">{device.online ? t('deviceOnline') : t('deviceOffline')}</span>
                  </div>
                  <p class="meta">
                    <code>{device.stableId}</code>
                    · {t('deviceLastSeen')}: {device.lastSeen ? formatTime(device.lastSeen) : t('deviceNever')}
                  </p>
                  <button type="button" class="button" aria-busy={revoking && revokeTarget?.stableId === device.stableId} onclick={() => planRevoke(device)}>
                    {t('revokePlanButton')}
                  </button>
                </li>
              {/each}
            </ul>
          {/if}

          <h3>{t('enrollHeading')}</h3>
          <p class="hint">{t('enrollIntro')}</p>
          {#if invitation}
            <div class="joinkey" role="group" aria-label={t('enrollJoinKeyHeading')}>
              <h4>{t('enrollJoinKeyHeading')}</h4>
              <p class="badge warn">{t('enrollJoinKeyOnce')}</p>
              <pre class="diff"><code>{invitation.joinKey}</code></pre>
              <dl class="evidence">
                <div><dt>{t('enrollIssuedBy')}</dt><dd>{invitation.issuedBy}</dd></div>
                <div><dt>{t('enrollExpiresAt')}</dt><dd>{formatTime(invitation.expiresAt)}</dd></div>
              </dl>
              {#if invitation.guidance}
                <h4>{t('enrollGuidanceHeading')}</h4>
                {#if invitation.guidance.clusterCaTrustRequired}
                  <p class="badge warn">{t('enrollCaRequired')}</p>
                {/if}
                <ol class="steps">
                  {#each invitation.guidance.steps as step}
                    <li>
                      {t(stepKey(step.kind))}
                      {#if step.elevationRequired}<span class="badge muted">{t('enrollElevationBadge')}</span>{/if}
                    </li>
                  {/each}
                </ol>
                <dl class="evidence">
                  <div><dt>{t('enrollGatewayLabel')}</dt><dd><code>{invitation.guidance.gatewayHostname}</code></dd></div>
                </dl>
                <p class="meta">{t('enrollHostsLabel')}: {invitation.guidance.operatorHostnames.join(', ')}</p>
              {/if}
              <button type="button" class="button" onclick={resetInvitation}>{t('enrollAnother')}</button>
            </div>
          {:else}
            <div class="enrollform">
              <label for="invite-label">{t('enrollLabelLabel')}</label>
              <input
                id="invite-label"
                type="text"
                bind:value={inviteLabel}
                placeholder={t('enrollLabelPlaceholder')}
                autocomplete="off"
              />
              <button type="button" class="button" aria-busy={inviting} disabled={!inviteLabel.trim()} onclick={createInvitation}>
                {t('enrollCreateButton')}
              </button>
            </div>
          {/if}

          <h3>{t('activityHeading')}</h3>
          {#if access.activity.length === 0}
            <p class="meta">{t('activityEmpty')}</p>
          {:else}
            <ul class="datasets">
              {#each access.activity as entry}
                <li class="dataset">
                  <div class="facet-head">
                    <strong>{entry.phase}</strong>
                    {#if entry.checkpoint}<span class="badge muted">{entry.checkpoint}</span>{/if}
                  </div>
                  {#if entry.evidenceSummary}<p class="reason">{entry.evidenceSummary}</p>{/if}
                  <p class="meta">{formatTime(entry.startedAt)}</p>
                </li>
              {/each}
            </ul>
          {/if}
        {/if}
      </section>
    {:else if selected}
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
                  {#if facet.remediationUrl}
                    <a href={facet.remediationUrl} target="_blank" rel="noopener noreferrer">
                      {t(routeLabels[facet.remediation.kind])}
                      <span class="sr-label"> ({t('opensNewTab')})</span>
                    </a>
                  {:else}
                    {t(routeLabels[facet.remediation.kind])}
                    {#if facet.remediation.reference}<code>{facet.remediation.reference}</code>{/if}
                  {/if}
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
  .viewnav {
    display: flex;
    gap: 0.5rem;
    margin-top: 1.25rem;
  }
  .viewnav button {
    padding: 0.4rem 0.9rem;
    border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
    border-radius: 0.5rem;
    background: transparent;
    color: inherit;
    cursor: pointer;
  }
  .viewnav button[aria-pressed='true'] {
    border-color: currentColor;
    font-weight: 700;
  }
  .roadmap {
    display: inline-block;
    margin: 0.5rem 0;
    border-style: dashed;
  }
  ul.datasets {
    list-style: none;
    padding: 0;
    margin: 0.75rem 0 0;
    display: grid;
    gap: 0.6rem;
  }
  .dataset {
    padding: 0.75rem 1rem;
    border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
    border-radius: 0.5rem;
  }
  dl.evidence {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
    gap: 0.25rem 1rem;
    margin: 0.5rem 0 0;
  }
  dl.evidence div {
    display: flex;
    gap: 0.5rem;
    justify-content: space-between;
    padding: 0.15rem 0;
    border-bottom: 1px solid color-mix(in srgb, currentColor 10%, transparent);
    font-size: 0.9rem;
  }
  dl.evidence dt {
    opacity: 0.75;
  }
  dl.evidence dd {
    margin: 0;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .offer {
    padding: 0.75rem 1rem;
    border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
    border-radius: 0.5rem;
  }
  .offer-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .offer .name {
    font-weight: 600;
  }
  .offer .button {
    margin-top: 0.6rem;
  }
  .fitline,
  .errorline {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    margin-top: 0.75rem;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin-top: 1rem;
  }
  .button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  pre.diff {
    margin: 0.5rem 0 0;
    padding: 0.75rem 1rem;
    border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
    border-radius: 0.5rem;
    background: color-mix(in srgb, currentColor 6%, transparent);
    overflow-x: auto;
    font-size: 0.82rem;
    line-height: 1.45;
  }
  .enrollform {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
    margin-top: 0.5rem;
  }
  .enrollform label {
    font-weight: 600;
  }
  .enrollform input {
    padding: 0.45rem 0.6rem;
    border: 1px solid color-mix(in srgb, currentColor 30%, transparent);
    border-radius: 0.4rem;
    background: transparent;
    color: inherit;
    font-size: 1rem;
    min-width: 14rem;
  }
  .enrollform .button {
    margin-top: 0;
  }
  .joinkey {
    margin-top: 0.5rem;
    padding: 1rem;
    border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
    border-radius: 0.5rem;
  }
  ol.steps {
    margin: 0.5rem 0 0;
    padding-left: 1.5rem;
    display: grid;
    gap: 0.35rem;
  }
  ol.steps .badge {
    margin-left: 0.4rem;
  }
  @media (prefers-reduced-motion: reduce) {
    * {
      animation: none !important;
    }
  }
</style>
