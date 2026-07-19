<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, initializeSession, type BootstrapAssetRequirements, type CapabilityCatalog, type CapabilityMode, type CapabilityPlanResult, type ChangePlan, type ClusterProfile, type CredentialMetadata, type GenericGitCredentialStatus, type GenericGitProposal, type GitHubTokenStatus, type NodeCapabilities, type NodeInspectionResult, type NodeProbeResult, type NodeTarget, type RecoveryBundlePreview, type SetupJourney, type VaultStatus, type WorkflowRun } from '$lib/api';
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
  let vaultStatus: VaultStatus | null = $state(null);
  let credentials: CredentialMetadata[] = $state([]);
  let vaultError = $state('');
  let vaultBusy = $state(false);
  let vaultPassphrase = $state('');
  let credentialValue = $state('');
  let credentialExpiresAt = $state('');
  let recoveryPassphrase = $state('');
  let recoveryRecipients = $state('');
  let recoveryIdentity = $state('');
  let recoveryCredentialMode: 'passphrase' | 'identity' = $state('passphrase');
  let recoveryBundle = $state('');
  let recoveryFileName = $state('');
  let recoveryPreview: RecoveryBundlePreview | null = $state(null);
  let recoveryBusy = $state(false);
  let recoveryError = $state('');
  let recoveryNotice = $state('');
  let capabilityCatalog: CapabilityCatalog | null = $state(null);
  let capabilityMode: CapabilityMode = $state('minimal');
  let capabilityApps: string[] = $state([]);
  let capabilityRelease = $state('v1.0.0');
  let capabilityRepositoryURL = $state('');
  let capabilityDomain = $state('');
  let capabilityPlan: CapabilityPlanResult | null = $state(null);
  let capabilityError = $state('');
  let capabilityBusy = $state(false);
  let gitHubToken = $state('');
  let gitHubAuthority: 'creation' | 'ongoing' = $state('creation');
  let gitHubStatus: GitHubTokenStatus | null = $state(null);
  let gitHubBusy = $state(false);
  let gitHubError = $state('');
  let gitHubRepositoryName = $state('smallworlds-overlay');
  let gitHubOverlayNotice = $state('');
  let genericGitUsername = $state('');
  let genericGitToken = $state('');
  let genericGitStatus: GenericGitCredentialStatus | null = $state(null);
  let genericGitBusy = $state(false);
  let genericGitError = $state('');
  let genericGitOverlayNotice = $state('');
  let genericGitProposal: GenericGitProposal | null = $state(null);
  let bootstrapAssets: BootstrapAssetRequirements | null = $state(null);
  let bootstrapAssetRelease = $state('v1.2.3');
  let bootstrapAssetError = $state('');
  let bootstrapAssetBusy = $state(false);
  let nodeCapabilities: NodeCapabilities | null = $state(null);
  let nodeTargetKind: 'remote' | 'same-host' = $state('remote');
  let nodeHost = $state('');
  let nodePort = $state(22);
  let nodeUsername = $state('root');
  let nodeAuthentication: 'agent' | 'private-key' | 'password' = $state('agent');
  let nodePassword = $state('');
  let nodePrivateKey = $state('');
  let nodeKeyPassphrase = $state('');
  let nodeSudoPassword = $state('');
  let nodeProbe: NodeProbeResult | null = $state(null);
  let nodeInspection: NodeInspectionResult | null = $state(null);
  let nodeError = $state('');
  let nodeBusy = $state(false);
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
      [profiles, vaultStatus, capabilityCatalog, nodeCapabilities] = await Promise.all([api.listProfiles(), api.getVaultStatus(), api.getCapabilities(), api.getNodeCapabilities()]);
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
    credentials = vaultStatus?.state === 'unlocked' ? await api.listCredentials(profile.id) : [];
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

  function vaultErrorMessage(code: string): string {
    switch (code) {
      case 'os_credential_store_unavailable': return message('osCredentialStoreUnavailable');
      case 'vault_passphrase_incorrect': return message('vaultPassphraseIncorrect');
      case 'vault_passphrase_too_short': return message('vaultPassphraseTooShort');
      case 'vault_wrapping_key_missing': return message('vaultWrappingKeyMissing');
      case 'credential_storage_failed': return message('credentialStorageFailed');
      case 'credential_removal_failed': return message('credentialRemovalFailed');
      default: return message('vaultUnlockFailed');
    }
  }

  async function unlockVault(method: 'operating-system' | 'passphrase'): Promise<void> {
    vaultBusy = true;
    vaultError = '';
    try {
      vaultStatus = await api.unlockVault(method, method === 'passphrase' ? vaultPassphrase : undefined);
      vaultPassphrase = '';
      credentials = activeProfile ? await api.listCredentials(activeProfile.id) : [];
    } catch (reason) {
      vaultError = vaultErrorMessage(reason instanceof Error ? reason.message : 'vault_unlock_failed');
    } finally {
      vaultBusy = false;
    }
  }

  async function storeCredential(): Promise<void> {
    if (!activeProfile) return;
    vaultBusy = true;
    vaultError = '';
    try {
      await api.storeCredential(activeProfile.id, credentialValue, credentialExpiresAt);
      credentialValue = '';
      credentials = await api.listCredentials(activeProfile.id);
    } catch (reason) {
      vaultError = vaultErrorMessage(reason instanceof Error ? reason.message : 'credential_storage_failed');
    } finally {
      vaultBusy = false;
    }
  }

  async function removeCredential(): Promise<void> {
    if (!activeProfile) return;
    vaultBusy = true;
    vaultError = '';
    try {
      await api.removeCredential(activeProfile.id);
      credentials = await api.listCredentials(activeProfile.id);
    } catch (reason) {
      vaultError = vaultErrorMessage(reason instanceof Error ? reason.message : 'credential_removal_failed');
    } finally {
      vaultBusy = false;
    }
  }

  function recoveryCredential(): { passphrase?: string; identity?: string } {
    return recoveryCredentialMode === 'passphrase' ? { passphrase: recoveryPassphrase } : { identity: recoveryIdentity };
  }

  function recoveryErrorMessage(code: string): string {
    switch (code) {
      case 'recovery_bundle_credentials_incorrect': return message('recoveryCredentialsIncorrect');
      case 'lifecycle_authority_already_exists': return message('recoveryAuthorityExists');
      case 'recovery_bundle_identity_mismatch': return message('recoveryIdentityMismatch');
      case 'vault_locked': return message('recoveryVaultLocked');
      default: return message('recoveryFailed');
    }
  }

  async function exportRecoveryBundle(): Promise<void> {
    if (!activeProfile) return;
    recoveryBusy = true;
    recoveryError = '';
    recoveryNotice = '';
    try {
      const recipients = recoveryRecipients.split(/\s+/).filter(Boolean);
      const encryption = recipients.length > 0 ? { recipients } : { passphrase: recoveryPassphrase };
      const bundle = await api.exportRecoveryBundle(activeProfile.id, encryption);
      const url = URL.createObjectURL(bundle);
      const download = document.createElement('a');
      download.href = url;
      download.download = `${activeProfile.name.replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '') || 'smallworlds'}-recovery.bundle`;
      download.click();
      URL.revokeObjectURL(url);
      recoveryPassphrase = '';
      recoveryRecipients = '';
      recoveryNotice = message('recoveryExported');
    } catch (reason) {
      recoveryError = recoveryErrorMessage(reason instanceof Error ? reason.message : 'recovery_failed');
    } finally {
      recoveryBusy = false;
    }
  }

  async function readRecoveryBundle(event: Event): Promise<void> {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    recoveryPreview = null;
    recoveryError = '';
    recoveryNotice = '';
    if (!file) {
      recoveryBundle = '';
      recoveryFileName = '';
      return;
    }
    recoveryFileName = file.name;
    const dataURL = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    });
    recoveryBundle = dataURL.slice(dataURL.indexOf(',') + 1);
  }

  async function previewRecoveryBundle(): Promise<void> {
    if (!recoveryBundle) return;
    recoveryBusy = true;
    recoveryError = '';
    recoveryNotice = '';
    try {
      recoveryPreview = await api.previewRecoveryBundle(recoveryBundle, recoveryCredential());
    } catch (reason) {
      recoveryPreview = null;
      recoveryError = recoveryErrorMessage(reason instanceof Error ? reason.message : 'recovery_failed');
    } finally {
      recoveryBusy = false;
    }
  }

  async function importRecoveryBundle(): Promise<void> {
    if (!recoveryBundle || !recoveryPreview) return;
    recoveryBusy = true;
    recoveryError = '';
    recoveryNotice = '';
    try {
      const imported = await api.importRecoveryBundle(recoveryBundle, recoveryPreview.profile.id, recoveryCredential());
      profiles = await api.listProfiles();
      const profile = profiles.find((candidate) => candidate.id === imported.profile.id);
      if (profile) {
        creating = false;
        editing = false;
        await selectProfile(profile);
      }
      recoveryPassphrase = '';
      recoveryIdentity = '';
      recoveryBundle = '';
      recoveryFileName = '';
      recoveryPreview = null;
      recoveryNotice = message('recoveryImported');
    } catch (reason) {
      recoveryError = recoveryErrorMessage(reason instanceof Error ? reason.message : 'recovery_failed');
    } finally {
      recoveryBusy = false;
    }
  }

  function toggleCapability(id: string, checked: boolean): void {
    capabilityApps = checked ? [...new Set([...capabilityApps, id])].sort() : capabilityApps.filter((app) => app !== id);
  }

  async function planCapabilities(): Promise<void> {
    if (!activeProfile) return;
    capabilityBusy = true;
    capabilityError = '';
    try {
      capabilityPlan = await api.planCapabilities({ profileId: activeProfile.id, mode: capabilityMode, communityIds: capabilityApps, release: capabilityRelease, repositoryUrl: capabilityRepositoryURL, domain: capabilityDomain });
      plan = capabilityPlan.plan;
    } catch (reason) {
      capabilityPlan = null;
      capabilityError = reason instanceof Error ? reason.message : 'invalid_capability_selection';
    } finally {
      capabilityBusy = false;
    }
  }

  async function validateGitHubToken(): Promise<void> {
    if (!activeProfile) return;
    gitHubBusy = true;
    gitHubError = '';
    try {
      gitHubStatus = await api.validateGitHubToken(activeProfile.id, gitHubToken, gitHubAuthority);
      gitHubToken = '';
    } catch (reason) {
      gitHubStatus = null;
      gitHubError = reason instanceof Error ? reason.message : 'github_token_validation_failed';
    } finally {
      gitHubBusy = false;
    }
  }

  async function establishGitHubOverlay(): Promise<void> {
    if (!activeProfile || !capabilityPlan) return;
    gitHubBusy = true;
    gitHubError = '';
    gitHubOverlayNotice = '';
    try {
      const identity = await api.establishGitHubOverlay({ profileId: activeProfile.id, planId: capabilityPlan.plan.id, repositoryName: gitHubRepositoryName, mode: capabilityMode, communityIds: capabilityApps, release: capabilityRelease, domain: capabilityDomain });
      gitHubOverlayNotice = `${identity.repositoryUrl} @ ${identity.commit}`;
    } catch (reason) {
      gitHubError = reason instanceof Error ? reason.message : 'github_overlay_failed';
    } finally {
      gitHubBusy = false;
    }
  }

  async function validateGenericGitCredentials(): Promise<void> {
    if (!activeProfile) return;
    genericGitBusy = true;
    genericGitError = '';
    try {
      genericGitStatus = await api.validateGenericGitCredentials(activeProfile.id, capabilityRepositoryURL, genericGitUsername, genericGitToken);
      genericGitToken = '';
    } catch (reason) {
      genericGitStatus = null;
      genericGitError = reason instanceof Error ? reason.message : 'generic_git_validation_failed';
    } finally {
      genericGitBusy = false;
    }
  }

  async function establishGenericGitOverlay(): Promise<void> {
    if (!activeProfile || !capabilityPlan) return;
    genericGitBusy = true;
    genericGitError = '';
    genericGitOverlayNotice = '';
    try {
      const identity = await api.establishGenericGitOverlay({ profileId: activeProfile.id, planId: capabilityPlan.plan.id, repositoryUrl: capabilityRepositoryURL, mode: capabilityMode, communityIds: capabilityApps, release: capabilityRelease, domain: capabilityDomain });
      genericGitOverlayNotice = `${identity.repositoryUrl} @ ${identity.commit}`;
    } catch (reason) {
      genericGitError = reason instanceof Error ? reason.message : 'generic_git_overlay_failed';
    } finally {
      genericGitBusy = false;
    }
  }

  async function proposeGenericGitOverlay(): Promise<void> {
    if (!activeProfile || !capabilityPlan) return;
    genericGitBusy = true;
    genericGitError = '';
    genericGitProposal = null;
    try {
      genericGitProposal = await api.proposeGenericGitOverlay({ profileId: activeProfile.id, planId: capabilityPlan.plan.id, repositoryUrl: capabilityRepositoryURL, mode: capabilityMode, communityIds: capabilityApps, release: capabilityRelease, domain: capabilityDomain });
    } catch (reason) {
      genericGitError = reason instanceof Error ? reason.message : 'generic_git_proposal_failed';
    } finally {
      genericGitBusy = false;
    }
  }

  async function inspectBootstrapAssets(): Promise<void> {
    bootstrapAssetBusy = true;
    bootstrapAssetError = '';
    try {
      bootstrapAssets = await api.getBootstrapAssetRequirements(bootstrapAssetRelease);
    } catch (reason) {
      bootstrapAssets = null;
      bootstrapAssetError = reason instanceof Error ? reason.message : 'bootstrap_asset_status_failed';
    } finally {
      bootstrapAssetBusy = false;
    }
  }

  async function acquireBootstrapAssets(): Promise<void> {
    bootstrapAssetBusy = true;
    bootstrapAssetError = '';
    try {
      bootstrapAssets = await api.acquireBootstrapAssets(bootstrapAssetRelease);
    } catch (reason) {
      bootstrapAssetError = reason instanceof Error ? reason.message : 'bootstrap_asset_acquisition_failed';
    } finally {
      bootstrapAssetBusy = false;
    }
  }

  function currentNodeTarget(): NodeTarget {
    return nodeTargetKind === 'same-host' ? { kind: 'same-host' } : { kind: 'remote', host: nodeHost, port: nodePort, username: nodeUsername };
  }

  async function probeNode(): Promise<void> {
    if (!activeProfile || nodeTargetKind !== 'remote') return;
    nodeBusy = true;
    nodeError = '';
    nodeProbe = null;
    try {
      nodeProbe = await api.probeNode(activeProfile.id, currentNodeTarget());
    } catch (reason) {
      nodeError = reason instanceof Error ? reason.message : 'node_host_key_probe_failed';
    } finally {
      nodeBusy = false;
    }
  }

  async function trustNode(): Promise<void> {
    if (!activeProfile || !nodeProbe) return;
    nodeBusy = true;
    nodeError = '';
    try {
      await api.trustNode(activeProfile.id, currentNodeTarget(), nodeProbe.fingerprint);
      nodeProbe = null;
    } catch (reason) {
      nodeError = reason instanceof Error ? reason.message : 'node_host_key_confirmation_required';
    } finally {
      nodeBusy = false;
    }
  }

  async function inspectNode(): Promise<void> {
    if (!activeProfile) return;
    nodeBusy = true;
    nodeError = '';
    nodeInspection = null;
    try {
      nodeInspection = await api.inspectNode(activeProfile.id, currentNodeTarget(), { kind: nodeAuthentication, ...(nodePassword ? { password: nodePassword } : {}), ...(nodePrivateKey ? { privateKey: nodePrivateKey } : {}), ...(nodeKeyPassphrase ? { keyPassphrase: nodeKeyPassphrase } : {}), ...(nodeSudoPassword ? { sudoPassword: nodeSudoPassword } : {}) });
      nodePassword = '';
      nodePrivateKey = '';
      nodeKeyPassphrase = '';
      nodeSudoPassword = '';
    } catch (reason) {
      nodeError = reason instanceof Error ? reason.message : 'node_inspection_failed';
    } finally {
      nodeBusy = false;
    }
  }

  function rotationLabel(status: string): string {
    if (status === 'expired') return message('rotationExpired');
    if (status === 'due-soon') return message('rotationDueSoon');
    return message('rotationCurrent');
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

      <section class="card recovery-card" aria-labelledby="recovery-title">
        <div class="vault-heading">
          <div>
            <p class="eyebrow">{message('recoveryEyebrow')}</p>
            <h2 id="recovery-title">{message('recoveryTitle')}</h2>
          </div>
        </div>
        <p class="muted">{message('recoveryDescription')}</p>
        {#if recoveryError}<p class="inline-error" role="alert">{recoveryError}</p>{/if}
        {#if recoveryNotice}<p class="inline-notice" aria-live="polite">{recoveryNotice}</p>{/if}

        {#if activeProfile}
          <form class="recovery-form" onsubmit={(event) => { event.preventDefault(); void exportRecoveryBundle(); }}>
            <h3>{message('recoveryExport')}</h3>
            <p class="muted">{message('recoveryExportDescription')}</p>
            <label>
              <span>{message('recoveryPassphrase')}</span>
              <input type="password" bind:value={recoveryPassphrase} minlength="12" autocomplete="new-password" placeholder={message('recoveryPassphraseHint')} />
            </label>
            <label>
              <span>{message('recoveryRecipients')}</span>
              <textarea bind:value={recoveryRecipients} rows="2" placeholder={message('recoveryRecipientsHint')}></textarea>
            </label>
            <p class="muted">{message('recoveryRecipientChoice')}</p>
            <div class="actions"><button type="submit" disabled={recoveryBusy || (!recoveryPassphrase && !recoveryRecipients)}>{message('recoveryDownload')}</button></div>
          </form>
        {/if}

        <form class="recovery-form" onsubmit={(event) => { event.preventDefault(); void previewRecoveryBundle(); }}>
          <h3>{message('recoveryImport')}</h3>
          <p class="muted">{message('recoveryImportDescription')}</p>
          <label>
            <span>{message('recoveryBundleFile')}</span>
            <input type="file" accept=".bundle,application/octet-stream" onchange={(event) => void readRecoveryBundle(event)} />
          </label>
          {#if recoveryFileName}<p class="muted">{recoveryFileName}</p>{/if}
          <label>
            <span>{message('recoveryUnlockMethod')}</span>
            <select bind:value={recoveryCredentialMode} onchange={() => { recoveryPreview = null; recoveryError = ''; }}>
              <option value="passphrase">{message('recoveryPassphrase')}</option>
              <option value="identity">{message('recoveryAgeIdentity')}</option>
            </select>
          </label>
          {#if recoveryCredentialMode === 'passphrase'}
            <label>
              <span>{message('recoveryPassphrase')}</span>
              <input type="password" bind:value={recoveryPassphrase} minlength="12" autocomplete="current-password" />
            </label>
          {:else}
            <label>
              <span>{message('recoveryAgeIdentity')}</span>
              <textarea bind:value={recoveryIdentity} rows="3" autocomplete="off"></textarea>
            </label>
          {/if}
          <div class="actions"><button type="submit" disabled={recoveryBusy || !recoveryBundle || (recoveryCredentialMode === 'passphrase' ? recoveryPassphrase.length < 12 : !recoveryIdentity)}>{message('recoveryPreview')}</button></div>
        </form>

        {#if recoveryPreview}
          <section class="recovery-preview" aria-labelledby="recovery-preview-title">
            <p class="eyebrow">{message('recoveryPreview')}</p>
            <h3 id="recovery-preview-title">{recoveryPreview.profile.name}</h3>
            <dl>
              <div><dt>{message('recoveryClusterId')}</dt><dd><code>{recoveryPreview.profile.id}</code></dd></div>
              <div><dt>{message('deploymentMode')}</dt><dd>{recoveryPreview.profile.deploymentMode}</dd></div>
              <div><dt>{message('recoveryFormat')}</dt><dd>{recoveryPreview.format} v{recoveryPreview.version}</dd></div>
            </dl>
            <p class="muted">{message('recoveryConfirmDescription')}</p>
            <div class="actions"><button type="button" onclick={() => void importRecoveryBundle()} disabled={recoveryBusy}>{message('recoveryConfirmImport')}</button></div>
          </section>
        {/if}
      </section>

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

        <section class="card capability-card" aria-labelledby="capability-title">
          <p class="eyebrow">{message('capabilityEyebrow')}</p>
          <h2 id="capability-title">{message('capabilityTitle')}</h2>
          <p class="muted">{message('capabilityDescription')}</p>
          {#if capabilityError}<p class="inline-error" role="alert">{capabilityError}</p>{/if}
          <form onsubmit={(event) => { event.preventDefault(); void planCapabilities(); }}>
            <div class="form-grid">
              <label><span>{message('capabilityMode')}</span><select bind:value={capabilityMode} onchange={() => capabilityPlan = null}><option value="minimal">{message('capabilityMinimal')}</option><option value="collaboration">{message('capabilityCollaboration')}</option><option value="full">{message('capabilityFull')}</option><option value="custom">{message('capabilityCustom')}</option></select></label>
              <label><span>{message('capabilityRelease')}</span><input bind:value={capabilityRelease} required pattern="v[0-9]+\.[0-9]+\.[0-9]+.*" /></label>
            </div>
            <div class="form-grid">
              <label><span>{message('capabilityRepository')}</span><input type="url" bind:value={capabilityRepositoryURL} required placeholder="https://github.com/example/private-overlay.git" /></label>
              <label><span>{message('capabilityDomain')}</span><input bind:value={capabilityDomain} required placeholder="home.example" /></label>
            </div>
            {#if capabilityMode === 'custom'}
              <fieldset><legend>{message('capabilityCommunityApps')}</legend>{#each capabilityCatalog?.capabilities.filter((entry) => entry.category === 'community-application') ?? [] as entry (entry.id)}<label class="check"><input type="checkbox" checked={capabilityApps.includes(entry.id)} onchange={(event) => toggleCapability(entry.id, (event.currentTarget as HTMLInputElement).checked)} /><span>{entry.id} · {entry.resources.memoryMi} MiB / {entry.resources.storageGi} GiB</span></label>{/each}</fieldset>
            {/if}
            <div class="actions"><button type="submit" disabled={capabilityBusy}>{message('capabilityReview')}</button></div>
          </form>
          {#if capabilityPlan}
            <section class="capability-preview" aria-labelledby="capability-preview-title"><p class="eyebrow">{message('capabilityPreview')}</p><h3 id="capability-preview-title">{message('capabilityPlanReady')}</h3><dl><div><dt>{message('capabilityMemory')}</dt><dd>{capabilityPlan.overlay.assessment.resources.memoryMi} MiB</dd></div><div><dt>{message('capabilityStorage')}</dt><dd>{capabilityPlan.overlay.assessment.resources.storageGi} GiB</dd></div><div><dt>{message('capabilityExposure')}</dt><dd>{capabilityPlan.overlay.assessment.exposure.join(', ')}</dd></div><div><dt>{message('capabilityProtection')}</dt><dd>{capabilityPlan.overlay.assessment.protection.join(', ')}</dd></div></dl><div data-testid="overlay-diff" class="overlay-diff" role="textbox" aria-readonly="true" tabindex="0" aria-label={message('capabilityOverlayDiff')}>{capabilityPlan.overlay.diff}</div></section>
          {/if}
        </section>

        <section class="card asset-card" aria-labelledby="asset-title">
          <p class="eyebrow">{message('bootstrapAssetEyebrow')}</p>
          <h2 id="asset-title">{message('bootstrapAssetTitle')}</h2>
          <p class="muted">{message('bootstrapAssetDescription')}</p>
          <p class="muted">{message('offlineBundleFuture')}</p>
          {#if bootstrapAssetError}<p class="inline-error" role="alert">{bootstrapAssetError === 'bootstrap_asset_release_unavailable' ? message('bootstrapAssetUnavailable') : bootstrapAssetError}</p>{/if}
          <form onsubmit={(event) => { event.preventDefault(); void inspectBootstrapAssets(); }}><label><span>{message('capabilityRelease')}</span><input bind:value={bootstrapAssetRelease} required pattern="v[0-9]+\.[0-9]+\.[0-9]+.*" /></label><div class="actions"><button type="submit" disabled={bootstrapAssetBusy}>{message('bootstrapAssetInspect')}</button></div></form>
          {#if bootstrapAssets}
            <dl class="credential-metadata">{#each bootstrapAssets.assets as asset (asset.id)}<div><dt>{asset.id}</dt><dd>{asset.destination} · {asset.state} · <code>{asset.sha256.slice(0, 16)}…</code></dd></div>{/each}</dl>
            <div class="actions"><button onclick={() => void acquireBootstrapAssets()} disabled={bootstrapAssetBusy || bootstrapAssets.assets.every((asset) => asset.state === 'ready')}>{message('bootstrapAssetAcquire')}</button></div>
          {/if}
        </section>

        <section class="card node-card" aria-labelledby="node-title">
          <p class="eyebrow">{message('nodeEyebrow')}</p>
          <h2 id="node-title">{message('nodeTitle')}</h2>
          <p class="muted">{message('nodeDescription')}</p>
          {#if nodeError}<p class="inline-error" role="alert">{nodeError}</p>{/if}
          <form onsubmit={(event) => { event.preventDefault(); void inspectNode(); }}>
            <label><span>{message('nodeTarget')}</span><select bind:value={nodeTargetKind}><option value="remote">{message('nodeRemote')}</option>{#if nodeCapabilities?.sameHostSupported}<option value="same-host">{message('nodeSameHost')}</option>{/if}</select></label>
            {#if nodeTargetKind === 'remote'}
              <div class="form-grid"><label><span>{message('nodeHost')}</span><input bind:value={nodeHost} required autocomplete="off" /></label><label><span>{message('nodePort')}</span><input type="number" bind:value={nodePort} min="1" max="65535" required /></label></div>
              <label><span>{message('nodeUsername')}</span><input bind:value={nodeUsername} required autocomplete="username" /></label>
              <label><span>{message('nodeAuthentication')}</span><select bind:value={nodeAuthentication}><option value="agent">{message('nodeAgent')}</option><option value="private-key">{message('nodePrivateKey')}</option><option value="password">{message('nodePassword')}</option></select></label>
              {#if nodeAuthentication === 'password'}<label><span>{message('nodePassword')}</span><input type="password" bind:value={nodePassword} required autocomplete="current-password" /></label>{:else if nodeAuthentication === 'private-key'}<label><span>{message('nodePrivateKey')}</span><textarea bind:value={nodePrivateKey} required autocomplete="off"></textarea></label><label><span>{message('nodeKeyPassphrase')}</span><input type="password" bind:value={nodeKeyPassphrase} autocomplete="off" /></label>{/if}
              <label><span>{message('nodeSudoPassword')}</span><input type="password" bind:value={nodeSudoPassword} autocomplete="off" /></label>
              <div class="actions"><button type="button" onclick={() => void probeNode()} disabled={nodeBusy}>{message('nodeProbe')}</button></div>
            {/if}
            {#if nodeProbe}<p class="inline-notice">{message('nodeFingerprint')}: <code>{nodeProbe.fingerprint}</code> <button type="button" onclick={() => void trustNode()} disabled={nodeBusy}>{message('nodeTrust')}</button></p>{/if}
            <div class="actions"><button type="submit" disabled={nodeBusy}>{message('nodeInspect')}</button></div>
          </form>
          {#if nodeInspection}<dl class="credential-metadata"><div><dt>{message('nodeOperatingSystem')}</dt><dd>{nodeInspection.report.operatingSystem} / {nodeInspection.report.architecture}</dd></div><div><dt>{message('nodeCapacity')}</dt><dd>{nodeInspection.report.capacity.memoryMi} MiB · {nodeInspection.report.capacity.diskGi} GiB</dd></div><div><dt>{message('nodeAssessment')}</dt><dd>{nodeInspection.assessment.ready ? message('nodeReady') : nodeInspection.assessment.blockers.map((blocker) => blocker.code).join(', ')}</dd></div></dl>{/if}
        </section>

        <section class="card github-card" aria-labelledby="github-title">
          <p class="eyebrow">{message('githubEyebrow')}</p>
          <h2 id="github-title">{message('githubTitle')}</h2>
          <p class="muted">{message('githubDescription')} <a href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer">{message('githubTokenGuide')}</a></p>
          {#if gitHubError}<p class="inline-error" role="alert">{gitHubError}</p>{/if}
          <form onsubmit={(event) => { event.preventDefault(); void validateGitHubToken(); }}>
            <label><span>{message('githubAuthority')}</span><select bind:value={gitHubAuthority}><option value="creation">{message('githubCreationAuthority')}</option><option value="ongoing">{message('githubOngoingAuthority')}</option></select></label>
            <label><span>{message('githubToken')}</span><input type="password" bind:value={gitHubToken} required autocomplete="off" /></label>
            <div class="actions"><button type="submit" disabled={gitHubBusy}>{message('githubValidate')}</button></div>
          </form>
          {#if gitHubStatus}<dl class="credential-metadata"><div><dt>{message('githubOwner')}</dt><dd>{gitHubStatus.owner}</dd></div><div><dt>{message('credentialExpires')}</dt><dd>{gitHubStatus.expiresAt || message('githubNoExpiry')}</dd></div><div><dt>{message('githubAuthority')}</dt><dd>{gitHubStatus.authority === 'creation' ? message('githubCreationAuthority') : message('githubOngoingAuthority')}</dd></div></dl>{/if}
          {#if capabilityPlan && gitHubStatus?.authority === 'creation'}
            <form class="github-establish" onsubmit={(event) => { event.preventDefault(); void establishGitHubOverlay(); }}><label><span>{message('githubRepositoryName')}</span><input bind:value={gitHubRepositoryName} required pattern="[A-Za-z0-9._-]+" /></label><div class="actions"><button type="submit" disabled={gitHubBusy}>{message('githubEstablish')}</button></div></form>
          {/if}
          {#if gitHubOverlayNotice}<p class="inline-notice" aria-live="polite">{gitHubOverlayNotice}</p>{/if}
        </section>

        <section class="card generic-git-card" aria-labelledby="generic-git-title">
          <p class="eyebrow">{message('genericGitEyebrow')}</p>
          <h2 id="generic-git-title">{message('genericGitTitle')}</h2>
          <p class="muted">{message('genericGitDescription')}</p>
          {#if genericGitError}<p class="inline-error" role="alert">{genericGitError}</p>{/if}
          <form onsubmit={(event) => { event.preventDefault(); void validateGenericGitCredentials(); }}>
            <div class="form-grid"><label><span>{message('genericGitUsername')}</span><input bind:value={genericGitUsername} required autocomplete="username" /></label><label><span>{message('genericGitToken')}</span><input type="password" bind:value={genericGitToken} required autocomplete="off" /></label></div>
            <div class="actions"><button type="submit" disabled={genericGitBusy}>{message('genericGitValidate')}</button></div>
          </form>
          {#if genericGitStatus}<p class="inline-notice">{genericGitStatus.repositoryUrl}</p>{/if}
          {#if capabilityPlan && genericGitStatus}
            <p class="muted">{message('genericGitApprovalHint')}</p>
            <form class="github-establish" onsubmit={(event) => { event.preventDefault(); void establishGenericGitOverlay(); }}><div class="actions"><button type="submit" disabled={genericGitBusy}>{message('genericGitEstablish')}</button></div></form>
            <form class="github-establish" onsubmit={(event) => { event.preventDefault(); void proposeGenericGitOverlay(); }}><div class="actions"><button class="secondary" type="submit" disabled={genericGitBusy}>{message('genericGitPropose')}</button></div></form>
          {/if}
          {#if genericGitOverlayNotice}<p class="inline-notice" aria-live="polite">{genericGitOverlayNotice}</p>{/if}
          {#if genericGitProposal}<p class="inline-notice" aria-live="polite">{message('genericGitManualMerge')} <code>{genericGitProposal.branch}</code> · {genericGitProposal.commit}</p>{/if}
        </section>

		<section class="card vault-card" aria-labelledby="vault-title">
			<div class="vault-heading">
				<div>
					<p class="eyebrow">{message('vaultTitle')}</p>
					<h2 id="vault-title">{message('vaultTitle')}</h2>
				</div>
				<span class:unlocked={vaultStatus?.state === 'unlocked'} class="badge">
					{vaultStatus?.state === 'unlocked' ? message('vaultUnlocked') : message('vaultLocked')}
				</span>
			</div>
			<p class="muted">{message('vaultDescription')}</p>
			{#if vaultError}<p class="inline-error" role="alert">{vaultError}</p>{/if}
			{#if vaultStatus?.state !== 'unlocked'}
				<p class="facility-state">
					<span aria-hidden="true">{vaultStatus?.osCredentialStoreAvailable ? '✓' : '!'}</span>
					{vaultStatus?.osCredentialStoreAvailable ? message('osStoreAvailable') : message('osStoreUnavailable')}
				</p>
				{#if vaultStatus?.osCredentialStoreAvailable}
					<button onclick={() => void unlockVault('operating-system')} disabled={vaultBusy}>{message('unlockWithOSStore')}</button>
				{/if}
				<div class="fallback">
					<h3>{message('passphraseFallback')}</h3>
					<p class="muted">{message('passphraseFallbackDescription')}</p>
					<form onsubmit={(event) => { event.preventDefault(); void unlockVault('passphrase'); }}>
						<label>
							<span>{message('vaultPassphrase')}</span>
							<input type="password" bind:value={vaultPassphrase} required minlength="12" autocomplete="current-password" />
						</label>
						<div class="actions"><button type="submit" disabled={vaultBusy}>{message('unlockVault')}</button></div>
					</form>
				</div>
			{:else}
				{#if credentials.length > 0}
					{#each credentials as credential (credential.kind)}
						<dl class="credential-metadata">
							<div><dt>{message('gitProviderToken')}</dt><dd><span class="badge">{credential.present ? message('credentialPresent') : message('noCredential')}</span></dd></div>
							<div><dt>{message('credentialSource')}</dt><dd>{credential.source === 'operator' ? message('sourceOperator') : credential.source}</dd></div>
							<div><dt>{message('credentialExpires')}</dt><dd>{credential.expiresAt}</dd></div>
							<div><dt>{message('rotationStatus')}</dt><dd>{rotationLabel(credential.rotationStatus)}</dd></div>
						</dl>
					{/each}
				{:else}
					<p class="muted">{message('noCredential')}</p>
				{/if}
				<form class="credential-form" onsubmit={(event) => { event.preventDefault(); void storeCredential(); }}>
					<label>
						<span>{message('gitProviderToken')}</span>
						<input type="password" bind:value={credentialValue} required autocomplete="off" />
					</label>
					<label>
						<span>{message('credentialExpiry')}</span>
						<input bind:value={credentialExpiresAt} required placeholder="2030-01-02T03:04:05Z" />
					</label>
					<div class="actions">
						{#if credentials.length > 0}<button type="button" class="danger" onclick={() => void removeCredential()} disabled={vaultBusy}>{message('removeCredential')}</button>{/if}
						<button type="submit" disabled={vaultBusy}>{credentials.length > 0 ? message('replaceCredential') : message('storeCredential')}</button>
					</div>
				</form>
			{/if}
		</section>

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
  :global(button), :global(input), :global(select), :global(textarea) { font: inherit; }
  :global(button) { border: 0; border-radius: .75rem; background: #176b45; color: white; padding: .72rem 1rem; font-weight: 700; cursor: pointer; }
  :global(button:hover) { background: #0f5737; }
  :global(button:focus-visible), :global(input:focus-visible), :global(select:focus-visible), :global(textarea:focus-visible), :global(a:focus-visible) { outline: 3px solid #ef9f27; outline-offset: 3px; }
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
  input, select, textarea { width: 100%; min-height: 2.8rem; border: 1px solid #9eb0a4; border-radius: .65rem; padding: .65rem .75rem; background: white; color: #17211b; }
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
	.vault-card { margin: 0 0 2rem; border-left: 5px solid #176b45; }
	.vault-heading { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
	.vault-heading h2 { margin-bottom: 0; }
	.badge.unlocked { background: #daf1e1; color: #145f3d; }
	.facility-state { display: flex; align-items: center; gap: .55rem; font-weight: 750; }
	.facility-state span { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border-radius: 50%; background: #e6eee8; }
	.fallback { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
	.fallback h3 { margin: 0; }
	.inline-error { padding: .8rem; border-radius: .65rem; background: #fff1ee; color: #78281f; }
	.credential-form { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
	.credential-metadata { margin: 1.25rem 0 0; }
	.recovery-card { margin: 0 0 2rem; border-left: 5px solid #ef9f27; }
	.capability-card { margin: 0 0 2rem; border-left: 5px solid #315c9a; }
	.capability-card fieldset { display: grid; gap: .6rem; border: 1px solid #dce5de; border-radius: .65rem; padding: 1rem; }
	.capability-card legend { font-weight: 750; }
	.capability-card .check { display: flex; align-items: center; gap: .65rem; font-weight: 600; }
	.capability-card .check input { width: 1.1rem; min-height: 1.1rem; }
	.capability-preview { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
	.overlay-diff { overflow: auto; max-height: 22rem; padding: 1rem; border-radius: .65rem; background: #14241b; color: #e9f4eb; white-space: pre; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
	.recovery-form { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
	.recovery-form h3, .recovery-preview h3 { margin: 0; }
	.recovery-preview { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
	.inline-notice { padding: .8rem; border-radius: .65rem; background: #e6f1e9; color: #145f3d; }
	button.danger { background: transparent; border: 1px solid #b5473b; color: #78281f; }
	button.danger:hover { background: #fff1ee; }
  dl { display: grid; gap: .8rem; }
  dl div { display: grid; grid-template-columns: minmax(8rem, 11rem) 1fr; gap: 1rem; border-top: 1px solid #e0e6e1; padding-top: .8rem; }
  dt { color: #617066; font-weight: 700; }
  dd { margin: 0; overflow-wrap: anywhere; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em; }
  .timeline { list-style: none; padding: 0; display: grid; gap: .65rem; }
  .timeline li { display: flex; align-items: center; gap: .7rem; }
  .timeline li span { width: .7rem; height: .7rem; border-radius: 50%; background: #176b45; }
  .muted { color: #5f6c64; }
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
