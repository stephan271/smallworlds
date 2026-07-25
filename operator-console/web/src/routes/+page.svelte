<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, initializeSession, type BootstrapAssetRequirements, type CapabilityCatalog, type CapabilityMode, type CapabilityPlanResult, type ChangePlan, type ClusterProfile, type CredentialMetadata, type FullDecommissionResult, type GenericGitCredentialStatus, type GenericGitProposal, type GitHubTokenStatus, type HandoffAssessment, type HetznerChangePlan, type HetznerPlanResult, type HetznerPresets, type HetznerPresetTier, type HetznerProject, type HetznerToolchain, type HetznerWorkspace, type TemporaryAccess, type NodeCapabilities, type NodeInspectionResult, type NodeProbeResult, type NodeTarget, type OffsitePlan, type OffsiteProposal, type OffsiteProtection, type PreserveDataDecommissionResult, type RecoveryBundlePreview, type SetupJourney, type TailscaleClientOffer, type VaultStatus, type WorkflowRun } from '$lib/api';
  import { decommissionCopy } from '$lib/decommission-copy';
  import { formatCurrency, formatDateTime, formatNumber } from '$lib/format';
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
  let bootstrapAssetRelease = $state('v1.2.27');
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
  let localBootstrapDomain = $state('');
  let localBootstrapEnvironment = $state('');
  let localBootstrapDataDirectory = $state('/var/lib/smallworlds-data');
  let localBootstrapNodeName = $state('smallworlds-local-node');
  let localBootstrapACMEEmail = $state('');
  let localBootstrapManageDNS = $state(false);
  let localPublicDNSToken = $state('');
  let localPublicRouterAcknowledged = $state(false);
  let localBootstrapSecrets = $state('');
  let localBootstrapError = $state('');
  let localBootstrapBusy = $state(false);
  let handoffAssessment: HandoffAssessment | null = $state(null);
  let handoffBaseDomain = $state('');
  let tailscaleOffer: TailscaleClientOffer | null = $state(null);
  let deviceTrustFingerprint = $state('');
  let firstOwnerChallenge = $state('');
  let handoffBusy = $state(false);
  let handoffError = $state('');
  let offsiteEndpoint = $state('');
  let offsiteRegion = $state('');
  let offsiteBucket = $state('');
  let offsiteAccessKey = $state('');
  let offsiteSecretKey = $state('');
  let offsiteAcknowledge = $state(false);
  let offsiteStatus: OffsiteProtection | null = $state(null);
  let offsitePlan: OffsitePlan | null = $state(null);
  let offsitePlanId = $state('');
  let offsiteProposal: OffsiteProposal | null = $state(null);
  let offsiteError = $state('');
  let offsiteBusy = $state(false);
  let hetznerTokenValue = $state('');
  let hetznerProject: HetznerProject | null = $state(null);
  let hetznerDomain = $state('');
  let hetznerEnvExt = $state('');
  let hetznerPresets: HetznerPresets | null = $state(null);
  let hetznerTier: HetznerPresetTier = $state('recommended');
  let hetznerLocation = $state('');
  let hetznerServerType = $state('');
  let hetznerVolumeGb = $state(0);
  let hetznerAdoptions: string[] = $state([]);
  let hetznerAcmeEmail = $state('');
  let hetznerOperatorAddress = $state('');
  let hetznerTemporaryAccess: TemporaryAccess | null = $state(null);
  let hetznerToolchain: HetznerToolchain | null = $state(null);
  let hetznerWorkspace: HetznerWorkspace | null = $state(null);
  let hetznerPlan: HetznerPlanResult | null = $state(null);
  let hetznerBusy = $state(false);
  let hetznerError = $state('');
  let decommissionPlan: PreserveDataDecommissionResult | null = $state(null);
  let decommissionRun: WorkflowRun | null = $state(null);
  let decommissionBusy = $state(false);
  let decommissionError = $state('');
  let fullDecommissionPlan: FullDecommissionResult | null = $state(null);
  let fullDecommissionRun: WorkflowRun | null = $state(null);
  let fullDecommissionConfirmation = $state('');
  let fullDecommissionOverride = $state(false);
  let fullDecommissionOverrideReason = $state('');
  let fullDecommissionBusy = $state(false);
  let fullDecommissionError = $state('');
  let activeStep = $state('capabilities');
  let creating = $state(true);
  let editing = $state(false);
  let busy = $state(false);
  let profileName = $state('');
  let profileLanguage: Locale = $state('en');
  let deploymentMode: 'hetzner' | 'local-lan' | 'local-public' = $state('local-lan');
  let eventSource: EventSource | null = null;
  let pollTimer: number | undefined;

  const message = (key: MessageKey) => translate(locale, key);
  const decommissionMessage = (key: Parameters<typeof decommissionCopy>[1]) => decommissionCopy(locale, key);

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
    handoffAssessment = null;
    tailscaleOffer = null;
    deviceTrustFingerprint = '';
    firstOwnerChallenge = '';
    handoffError = '';
    offsitePlan = null;
    offsitePlanId = '';
    offsiteProposal = null;
    offsiteError = '';
    offsiteAcknowledge = false;
    try {
      offsiteStatus = await api.getOffsiteProtection(profile.id);
    } catch {
      offsiteStatus = null;
    }
    hetznerTokenValue = '';
    hetznerPresets = null;
    hetznerPlan = null;
    hetznerAdoptions = [];
    hetznerError = '';
    hetznerProject = null;
    hetznerToolchain = null;
    hetznerWorkspace = null;
    hetznerTemporaryAccess = null;
    fullDecommissionPlan = null;
    fullDecommissionRun = null;
    fullDecommissionConfirmation = '';
    fullDecommissionOverride = false;
    fullDecommissionOverrideReason = '';
    fullDecommissionError = '';
    if (profile.deploymentMode === 'hetzner') {
      try {
        hetznerProject = await api.getHetznerProject(profile.id);
        hetznerDomain = hetznerProject.naming?.domain ?? '';
        hetznerEnvExt = hetznerProject.naming?.envExt ?? '';
        hetznerToolchain = hetznerProject.toolchain ?? null;
        hetznerWorkspace = hetznerProject.workspace ?? null;
        hetznerTemporaryAccess = hetznerProject.temporaryAccess ?? null;
      } catch {
        hetznerProject = null;
      }
    }
    if (profile.deploymentMode === 'local-lan') {
      try {
        handoffAssessment = await api.getHandoffAssessment(profile.id);
      } catch {
        handoffAssessment = null;
      }
    }
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

  function handoffStepLabel(name: string): string {
    switch (name) {
      case 'cluster-ca-trust-installed': return message('handoffStepClusterCA');
      case 'private-network': return message('handoffStepPrivateNetwork');
      case 'launcher-enrolled': return message('handoffStepLauncherEnrolled');
      case 'gateway-identity': return message('handoffStepGatewayIdentity');
      case 'gateway-access-enforced': return message('handoffStepGatewayAccess');
      case 'handoff-verified': return message('handoffStepVerified');
      case 'temporary-access-closed': return message('handoffStepClosed');
      case 'first-owner-registered': return message('handoffStepFirstOwner');
      default: return name;
    }
  }

  async function refreshHandoffAssessment(): Promise<void> {
    if (activeProfile?.deploymentMode === 'local-lan') {
      handoffAssessment = await api.getHandoffAssessment(activeProfile.id);
    }
  }

  async function runHandoff(action: () => Promise<unknown>): Promise<void> {
    if (!activeProfile) return;
    handoffBusy = true;
    handoffError = '';
    try {
      await action();
      await refreshHandoffAssessment();
    } catch (reason) {
      handoffError = reason instanceof Error ? reason.message : 'request_failed';
    } finally {
      handoffBusy = false;
    }
  }

  const establishClusterCA = () => runHandoff(() => api.establishClusterCA(activeProfile!.id));
  const installDeviceTrust = () => runHandoff(async () => { deviceTrustFingerprint = (await api.installClusterCADeviceTrust(activeProfile!.id)).fingerprint; });
  const establishPrivateNetwork = () => runHandoff(() => api.establishPrivateNetwork(activeProfile!.id, handoffBaseDomain));
  const detectTailscale = () => runHandoff(async () => { tailscaleOffer = await api.getTailscaleClient(); });
  const establishEnrollment = () => runHandoff(() => api.establishEnrollment(activeProfile!.id));
  const consumeLauncherEnrollment = () => runHandoff(() => api.consumeLauncherEnrollment(activeProfile!.id));
  const verifyHandoff = () => runHandoff(() => api.verifyHandoff(activeProfile!.id));
  const closeTemporaryAccess = () => runHandoff(() => api.closeTemporaryAccess(activeProfile!.id));
  const claimFirstOwner = () => runHandoff(async () => { firstOwnerChallenge = (await api.claimFirstOwner(activeProfile!.id)).claim.challenge; });
  const registerFirstOwner = () => runHandoff(async () => {
    if (!firstOwnerChallenge) throw new Error('first_owner_claim_required');
    const created = await navigator.credentials.create({
      publicKey: {
        challenge: base64urlToBytes(firstOwnerChallenge),
        rp: { id: window.location.hostname, name: 'SmallWorlds Operator Console' },
        user: { id: crypto.getRandomValues(new Uint8Array(16)), name: 'console-owner', displayName: 'Console Owner' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }, { type: 'public-key', alg: -8 }, { type: 'public-key', alg: -257 }],
        authenticatorSelection: { userVerification: 'preferred', residentKey: 'required' },
        attestation: 'none',
        timeout: 60000
      }
    });
    if (!created) throw new Error('passkey_registration_cancelled');
    const credential = created as PublicKeyCredential;
    const attestation = credential.response as AuthenticatorAttestationResponse;
    await api.registerFirstOwner(activeProfile!.id, {
      credentialId: bytesToBase64url(new Uint8Array(credential.rawId)),
      clientDataJson: bytesToBase64url(new Uint8Array(attestation.clientDataJSON)),
      attestationObject: bytesToBase64url(new Uint8Array(attestation.attestationObject))
    });
  });

  function bytesToBase64url(bytes: Uint8Array): string {
    let binary = '';
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function base64urlToBytes(value: string): Uint8Array<ArrayBuffer> {
    const binary = atob(value.replace(/-/g, '+').replace(/_/g, '/'));
    const bytes = new Uint8Array(new ArrayBuffer(binary.length));
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    return bytes;
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

  async function inspectOffsiteDestination(): Promise<void> {
    if (!activeProfile) return;
    offsiteBusy = true;
    offsiteError = '';
    offsitePlan = null;
    offsitePlanId = '';
    offsiteProposal = null;
    try {
      offsiteStatus = await api.inspectOffsiteDestination({ profileId: activeProfile.id, endpoint: offsiteEndpoint, region: offsiteRegion, bucket: offsiteBucket, accessKeyId: offsiteAccessKey, secretAccessKey: offsiteSecretKey });
      offsiteAccessKey = '';
      offsiteSecretKey = '';
      if (!offsiteStatus.requiresAcknowledgement) offsiteAcknowledge = false;
    } catch (reason) {
      offsiteError = reason instanceof Error ? reason.message : 'offsite_inspection_failed';
    } finally {
      offsiteBusy = false;
    }
  }

  async function planOffsiteProtection(): Promise<void> {
    if (!activeProfile) return;
    offsiteBusy = true;
    offsiteError = '';
    offsiteProposal = null;
    try {
      offsitePlan = await api.planOffsiteProtection(activeProfile.id, offsiteAcknowledge);
      offsitePlanId = (offsitePlan.plan as { id?: string } | undefined)?.id ?? '';
    } catch (reason) {
      offsitePlan = null;
      offsiteError = reason instanceof Error ? reason.message : 'offsite_plan_failed';
    } finally {
      offsiteBusy = false;
    }
  }

  async function proposeOffsiteProtection(): Promise<void> {
    if (!activeProfile || !offsitePlanId) return;
    offsiteBusy = true;
    offsiteError = '';
    try {
      // The proposal opens only after the plan is approved; the credential values
      // reach the Cluster Secret, never Git or the browser.
      await api.approvePlan(offsitePlanId);
      offsiteProposal = await api.proposeOffsiteProtection(activeProfile.id, offsitePlanId);
      offsiteStatus = await api.getOffsiteProtection(activeProfile.id);
    } catch (reason) {
      offsiteError = reason instanceof Error ? reason.message : 'offsite_proposal_failed';
    } finally {
      offsiteBusy = false;
    }
  }

  async function validateOffsiteProtection(): Promise<void> {
    if (!activeProfile) return;
    offsiteBusy = true;
    offsiteError = '';
    try {
      const planned = await api.validateOffsiteProtection(activeProfile.id);
      let current = await api.approvePlan(planned.plan.id);
      for (let attempt = 0; attempt < 40 && current.state === 'running'; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 150));
        current = await api.getRun(current.id);
      }
      offsiteStatus = await api.getOffsiteProtection(activeProfile.id);
    } catch (reason) {
      offsiteError = reason instanceof Error ? reason.message : 'offsite_validation_failed';
    } finally {
      offsiteBusy = false;
    }
  }

  // The token is handed to the launcher once. It is custodied in the Launcher
  // Vault, and only a fingerprint-bearing verdict comes back — so the field is
  // cleared immediately and the value is never held in component state.
  async function validateHetznerToken(): Promise<void> {
    if (!activeProfile) return;
    hetznerBusy = true;
    hetznerError = '';
    try {
      const assessment = await api.validateHetznerToken(activeProfile.id, hetznerTokenValue);
      hetznerTokenValue = '';
      hetznerProject = { ...(hetznerProject ?? {}), token: assessment };
      if (assessment.state === 'valid') hetznerProject = await api.getHetznerProject(activeProfile.id);
    } catch (reason) {
      hetznerError = reason instanceof Error ? reason.message : 'hetzner_token_validation_failed';
    } finally {
      hetznerBusy = false;
    }
  }

  async function inspectHetznerProject(): Promise<void> {
    if (!activeProfile) return;
    hetznerBusy = true;
    hetznerError = '';
    hetznerPlan = null;
    try {
      hetznerProject = await api.inspectHetznerProject(activeProfile.id, hetznerDomain, hetznerEnvExt);
      hetznerAdoptions = [];
      await loadHetznerPresets();
    } catch (reason) {
      hetznerError = reason instanceof Error ? reason.message : 'hetzner_inspection_failed';
    } finally {
      hetznerBusy = false;
    }
  }

  async function loadHetznerPresets(): Promise<void> {
    if (!activeProfile) return;
    hetznerPresets = await api.getHetznerPresets({ profileId: activeProfile.id, mode: capabilityMode, communityIds: capabilityApps, location: hetznerLocation });
    hetznerLocation = hetznerPresets.location ?? hetznerLocation;
    const recommended = (hetznerPresets.presets ?? []).find((preset) => preset.tier === 'recommended');
    if (recommended && !hetznerServerType) {
      hetznerServerType = recommended.serverType ?? '';
      hetznerVolumeGb = recommended.volumeGb ?? 0;
    }
  }

  async function acquireHetznerToolchain(): Promise<void> {
    if (!activeProfile) return;
    hetznerBusy = true;
    hetznerError = '';
    try {
      const acquired = await api.acquireHetznerToolchain(activeProfile.id);
      hetznerToolchain = acquired.toolchain;
      hetznerWorkspace = acquired.workspace;
    } catch (reason) {
      hetznerError = reason instanceof Error ? reason.message : 'hetzner_toolchain_unavailable';
      // A refusal still carries the pinned versions and the prepared workspace.
      try {
        const current = await api.getHetznerProject(activeProfile.id);
        hetznerToolchain = current.toolchain ?? null;
        hetznerWorkspace = current.workspace ?? null;
      } catch {
        hetznerToolchain = null;
      }
    } finally {
      hetznerBusy = false;
    }
  }

  async function planHetznerInfrastructure(): Promise<void> {
    if (!activeProfile) return;
    hetznerBusy = true;
    hetznerError = '';
    try {
      hetznerPlan = await api.planHetznerInfrastructure({
        profileId: activeProfile.id,
        mode: capabilityMode,
        communityIds: capabilityApps,
        tier: hetznerTier,
        location: hetznerLocation,
        ...(hetznerTier === 'advanced' ? { serverType: hetznerServerType, volumeGb: hetznerVolumeGb } : {}),
        adoptions: hetznerAdoptions,
        acmeEmail: hetznerAcmeEmail
      });
      // Planning an approvable change opens the temporary administration path,
      // so the Operator can see and narrow it straight away.
      hetznerTemporaryAccess = (await api.getHetznerProject(activeProfile.id)).temporaryAccess ?? null;
    } catch (reason) {
      hetznerPlan = null;
      hetznerError = reason instanceof Error ? reason.message : 'hetzner_plan_failed';
    } finally {
      hetznerBusy = false;
    }
  }

  async function planPreserveDataDecommission(): Promise<void> {
    if (!activeProfile) return;
    decommissionBusy = true;
    decommissionError = '';
    try {
      decommissionPlan = await api.planPreserveDataDecommission(activeProfile.id);
      decommissionRun = null;
    } catch (reason) {
      decommissionError = reason instanceof Error ? reason.message : 'decommission_plan_failed';
    } finally {
      decommissionBusy = false;
    }
  }

  async function approvePreserveDataDecommission(): Promise<void> {
    if (!decommissionPlan) return;
    decommissionBusy = true;
    decommissionError = '';
    try {
      decommissionRun = await api.approvePlan(decommissionPlan.plan.id);
    } catch (reason) {
      decommissionError = reason instanceof Error ? reason.message : 'decommission_approval_failed';
    } finally {
      decommissionBusy = false;
    }
  }

  async function resumePreserveDataDecommission(): Promise<void> {
    if (!decommissionRun) return;
    decommissionBusy = true;
    decommissionError = '';
    try {
      decommissionRun = await api.resumePreserveDataDecommission(decommissionRun.id);
    } catch (reason) {
      decommissionError = reason instanceof Error ? reason.message : 'decommission_resume_failed';
    } finally {
      decommissionBusy = false;
    }
  }

  async function planFullDecommission(): Promise<void> {
    if (!activeProfile) return;
    fullDecommissionBusy = true;
    fullDecommissionError = '';
    try {
      fullDecommissionPlan = await api.planFullDecommission(activeProfile.id);
      fullDecommissionRun = null;
      fullDecommissionConfirmation = '';
      fullDecommissionOverride = false;
      fullDecommissionOverrideReason = '';
    } catch (reason) {
      fullDecommissionError = reason instanceof Error ? reason.message : 'full_decommission_plan_failed';
    } finally {
      fullDecommissionBusy = false;
    }
  }

  async function approveFullDecommission(): Promise<void> {
    if (!fullDecommissionPlan) return;
    fullDecommissionBusy = true;
    fullDecommissionError = '';
    try {
      fullDecommissionRun = await api.approveFullDecommission({
        planId: fullDecommissionPlan.plan.id,
        profileId: fullDecommissionPlan.decommission.profileId,
        planDigest: fullDecommissionPlan.decommission.digest,
        confirmation: fullDecommissionConfirmation,
        ownerOverride: fullDecommissionOverride,
        overrideReason: fullDecommissionOverrideReason
      });
      scheduleFullDecommissionPoll(fullDecommissionRun.id);
    } catch (reason) {
      fullDecommissionError = reason instanceof Error ? reason.message : 'full_decommission_approval_failed';
    } finally {
      fullDecommissionBusy = false;
    }
  }

  async function resumeFullDecommission(): Promise<void> {
    if (!fullDecommissionRun) return;
    fullDecommissionBusy = true;
    fullDecommissionError = '';
    try {
      fullDecommissionRun = await api.resumeFullDecommission(fullDecommissionRun.id);
      scheduleFullDecommissionPoll(fullDecommissionRun.id);
    } catch (reason) {
      fullDecommissionError = reason instanceof Error ? reason.message : 'full_decommission_resume_failed';
    } finally {
      fullDecommissionBusy = false;
    }
  }

  function exportFullDecommissionActivity(): void {
    if (!activeProfile) return;
    window.open(`/api/v1/full-decommission/activity?profileId=${encodeURIComponent(activeProfile.id)}`, '_blank', 'noopener');
  }

  function scheduleFullDecommissionPoll(runID: string): void {
    if (pollTimer) window.clearTimeout(pollTimer);
    pollTimer = window.setTimeout(async () => {
      try {
        fullDecommissionRun = await api.getRun(runID);
        if (fullDecommissionRun.state === 'running' && !fullDecommissionRun.currentCheckpoint.includes('interrupted')) scheduleFullDecommissionPoll(runID);
      } catch (reason) {
        fullDecommissionError = reason instanceof Error ? reason.message : 'full_decommission_status_failed';
      }
    }, 1000);
  }

  async function forgetActiveProfile(): Promise<void> {
    if (!activeProfile || !window.confirm(decommissionMessage('forgetConfirm').replace('{name}', activeProfile.name))) return;
    decommissionBusy = true;
    decommissionError = '';
    try {
      await api.forgetProfile(activeProfile.id);
      profiles = profiles.filter((profile) => profile.id !== activeProfile!.id);
      activeProfile = profiles[0] ?? null;
      decommissionPlan = null;
      decommissionRun = null;
      if (activeProfile) await selectProfile(activeProfile);
      else creating = true;
    } catch (reason) {
      decommissionError = reason instanceof Error ? reason.message : 'profile_forget_failed';
    } finally {
      decommissionBusy = false;
    }
  }

  // Narrowing re-derives the scope from an address the Operator supplies. The
  // launcher decides whether that address can actually serve as a scope, so a
  // response reporting the path still open is a normal outcome, not an error.
  async function narrowHetznerTemporaryAccess(): Promise<void> {
    if (!activeProfile) return;
    hetznerBusy = true;
    hetznerError = '';
    try {
      hetznerTemporaryAccess = await api.narrowHetznerTemporaryAccess(activeProfile.id, hetznerOperatorAddress);
    } catch (reason) {
      hetznerError = reason instanceof Error ? reason.message : 'temporary_access_failed';
    } finally {
      hetznerBusy = false;
    }
  }

  function hetznerAccessReasonLabel(reasonKey: string | undefined): string {
    switch (reasonKey) {
      case 'scoped-to-operator-address': return message('hetznerAccessReasonScoped');
      case 'operator-address-not-observed': return message('hetznerAccessReasonUnobserved');
      case 'operator-address-not-publicly-routable': return message('hetznerAccessReasonNotRoutable');
      case 'operator-address-carrier-grade-nat': return message('hetznerAccessReasonShared');
      default: return reasonKey ?? '';
    }
  }

  // Approval is the first step that can change the project, so it is a separate
  // explicit action on an unblocked plan.
  async function approveHetznerPlan(): Promise<void> {
    if (!activeProfile || !hetznerPlan?.plan?.id) return;
    hetznerBusy = true;
    hetznerError = '';
    try {
      run = await api.approvePlan(hetznerPlan.plan.id);
      window.localStorage.setItem(`smallworlds.run.${activeProfile.id}`, run.id);
      startEventStream(activeProfile.id);
      schedulePoll(run.id);
    } catch (reason) {
      hetznerError = reason instanceof Error ? reason.message : 'hetzner_plan_approval_failed';
    } finally {
      hetznerBusy = false;
    }
  }

  function toggleHetznerAdoption(providerId: string | undefined): void {
    if (!providerId) return;
    hetznerAdoptions = hetznerAdoptions.includes(providerId)
      ? hetznerAdoptions.filter((candidate) => candidate !== providerId)
      : [...hetznerAdoptions, providerId];
  }

  function hetznerTokenLabel(state: string | undefined): string {
    const labels: Record<string, MessageKey> = { valid: 'hetznerTokenValid', malformed: 'hetznerTokenMalformed', unauthorized: 'hetznerTokenUnauthorized', 'read-only': 'hetznerTokenReadOnly', inconclusive: 'hetznerTokenInconclusive', 'project-mismatch': 'hetznerTokenProjectMismatch' };
    return state && labels[state] ? message(labels[state]) : '';
  }

  function hetznerOwnershipLabel(ownership: string | undefined): string {
    const labels: Record<string, MessageKey> = { shared: 'hetznerOwnershipShared', 'profile-owned': 'hetznerOwnershipProfileOwned', adoptable: 'hetznerOwnershipAdoptable', conflicting: 'hetznerOwnershipConflicting', unknown: 'hetznerOwnershipUnknown', absent: 'hetznerOwnershipAbsent' };
    return ownership && labels[ownership] ? message(labels[ownership]) : (ownership ?? '');
  }

  function hetznerActionLabel(action: string | undefined): string {
    const labels: Record<string, MessageKey> = { create: 'hetznerActionCreate', adopt: 'hetznerActionAdopt', 'reuse-shared': 'hetznerActionReuseShared', keep: 'hetznerActionKeep', blocked: 'hetznerActionBlocked' };
    return action && labels[action] ? message(labels[action]) : (action ?? '');
  }

  function hetznerDelegationLabel(status: string | undefined): string {
    const labels: Record<string, MessageKey> = { confirmed: 'hetznerDelegationConfirmed', partial: 'hetznerDelegationPartial', missing: 'hetznerDelegationMissing', unknown: 'hetznerDelegationUnknown', 'not-required': 'hetznerDelegationNotRequired' };
    return status && labels[status] ? message(labels[status]) : '';
  }

  function hetznerBlockerLabel(code: string | undefined): string {
    const labels: Record<string, MessageKey> = { 'adoption-decision-required': 'hetznerBlockerAdoption', 'ownership-conflict': 'hetznerBlockerConflict', 'similar-name-unresolved': 'hetznerBlockerSimilar', 'nameserver-delegation-required': 'hetznerBlockerDelegation', 'server-type-unavailable': 'hetznerBlockerUnavailable', 'capacity-below-selected-capabilities': 'hetznerBlockerCapacity', 'inspection-incomplete': 'hetznerBlockerIncomplete', 'shared-prerequisite-missing': 'hetznerBlockerSharedPrerequisite' };
    return code && labels[code] ? message(labels[code]) : (code ?? '');
  }

  function hetznerCostNoteLabel(note: string): string {
    const labels: Record<string, MessageKey> = { 'volume-can-grow-but-never-shrink': 'hetznerCostNoteVolumeGrows', 'volume-remains-billable-until-deleted': 'hetznerCostNoteVolumeBillable', 'primary-ip-remains-billable-while-reserved': 'hetznerCostNotePrimaryIP', 'snapshots-and-backups-billed-separately': 'hetznerCostNoteSnapshots', 'prices-exclude-vat-and-traffic-overage': 'hetznerCostNoteVAT', 'estimate-from-observed-provider-catalog': 'hetznerCostNoteObserved' };
    return labels[note] ? message(labels[note]) : note;
  }

  function hetznerPresetLabel(tier: string | undefined): string {
    const labels: Record<string, MessageKey> = { small: 'hetznerPresetSmall', recommended: 'hetznerPresetRecommended', high: 'hetznerPresetHigh', advanced: 'hetznerPresetAdvanced' };
    return tier && labels[tier] ? message(labels[tier]) : (tier ?? '');
  }

  function hetznerPlanCost(plan: HetznerChangePlan | undefined): string {
    const total = plan?.cost?.totalMonthlyEur;
    return formatCurrency(locale, total, plan?.cost?.currency ?? 'EUR');
  }

  function offsiteVersioningLabel(value: string | undefined): string {
    const labels: Record<string, MessageKey> = { enabled: 'offsiteVersioningEnabled', disabled: 'offsiteVersioningDisabled', unsupported: 'offsiteVersioningUnsupported', unknown: 'offsiteVersioningUnknown' };
    return value && labels[value] ? message(labels[value]) : (value ?? '');
  }

  function offsiteResultLabel(result: string | undefined): string {
    const labels: Record<string, MessageKey> = { 'offsite-verified': 'offsiteResultVerified', 'local-backup-failed': 'offsiteResultLocalBackupFailed', 'replication-failed': 'offsiteResultReplicationFailed', 'no-offsite-evidence': 'offsiteResultNoEvidence', 'offsite-evidence-stale': 'offsiteResultStale', 'versioning-unsupported': 'offsiteResultVersioningUnsupported', pending: 'offsiteResultPending' };
    return result && labels[result] ? message(labels[result]) : (result ?? '');
  }

  function offsiteRemediationLabel(key: string | undefined): string {
    const labels: Record<string, MessageKey> = { 'offsite.remediation.none': 'offsiteRemediationNone', 'offsite.remediation.local_backup_failed': 'offsiteRemediationLocalBackupFailed', 'offsite.remediation.replication_failed': 'offsiteRemediationReplicationFailed', 'offsite.remediation.no_offsite_evidence': 'offsiteRemediationNoEvidence', 'offsite.remediation.evidence_stale': 'offsiteRemediationStale', 'offsite.remediation.versioning_unsupported': 'offsiteRemediationVersioningUnsupported', 'offsite.remediation.pending': 'offsiteRemediationPending' };
    return key && labels[key] ? message(labels[key]) : (key ?? '');
  }

  function offsiteImplicationLabel(code: string | undefined): string {
    const labels: Record<string, MessageKey> = { 'offsite-copy-of-all-buckets-created': 'offsiteImplData', 'offsite-storage-and-egress-billed-by-destination': 'offsiteImplCost', 'enables-offsite-disaster-protection': 'offsiteImplProtection' };
    return code && labels[code] ? message(labels[code]) : (code ?? '');
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
      nodeInspection = await api.inspectNode(activeProfile.id, currentNodeTarget(), { kind: nodeAuthentication, ...(nodePassword ? { password: nodePassword } : {}), ...(nodePrivateKey ? { privateKey: nodePrivateKey } : {}), ...(nodeKeyPassphrase ? { keyPassphrase: nodeKeyPassphrase } : {}), ...(nodeSudoPassword ? { sudoPassword: nodeSudoPassword } : {}) }, localBootstrapDataDirectory);
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

  async function planNodeSSHKey(): Promise<void> {
    if (!activeProfile) return;
    nodeBusy = true;
    nodeError = '';
    try {
      plan = await api.planNodeSSHKey(activeProfile.id);
    } catch (reason) {
      nodeError = reason instanceof Error ? reason.message : 'node_ssh_key_plan_failed';
    } finally {
      nodeBusy = false;
    }
  }

  async function planLocalBootstrap(): Promise<void> {
    if (!activeProfile) return;
    localBootstrapBusy = true;
    localBootstrapError = '';
    try {
      const result = await api.planLocalBootstrap({
        profileId: activeProfile.id,
        target: currentNodeTarget(),
        authentication: { kind: nodeAuthentication, ...(nodePassword ? { password: nodePassword } : {}), ...(nodePrivateKey ? { privateKey: nodePrivateKey } : {}), ...(nodeKeyPassphrase ? { keyPassphrase: nodeKeyPassphrase } : {}), ...(nodeSudoPassword ? { sudoPassword: nodeSudoPassword } : {}) },
        release: 'v1.2.27',
        configuration: { domain: localBootstrapDomain, environmentExtension: localBootstrapEnvironment, dataDirectory: localBootstrapDataDirectory, nodeName: localBootstrapNodeName, acmeEmail: localBootstrapACMEEmail, manageDns: localBootstrapManageDNS },
        ...(activeProfile.deploymentMode === 'local-public' ? { publicExposure: { dns01Provider: 'hetzner' as const, dnsZone: localBootstrapDomain, dnsToken: localPublicDNSToken, publicIpBehavior: 'dynamic-ddns' as const, routerAcknowledged: localPublicRouterAcknowledged } } : {}),
        ...(localBootstrapSecrets ? { secretsManifest: localBootstrapSecrets } : {})
      });
      plan = result.plan;
      nodeInspection = result.inspection;
      localBootstrapSecrets = '';
      localPublicDNSToken = '';
      nodePassword = '';
      nodePrivateKey = '';
      nodeKeyPassphrase = '';
      nodeSudoPassword = '';
    } catch (reason) {
      localBootstrapError = reason instanceof Error ? reason.message : 'local_bootstrap_plan_failed';
    } finally {
      localBootstrapBusy = false;
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

  async function cancelRun(): Promise<void> {
    if (!run || run.state !== 'running') return;
    busy = true;
    error = '';
    try {
      run = await api.cancelRun(run.id);
      schedulePoll(run.id);
    } catch (reason) {
      error = reason instanceof Error ? reason.message : 'run_cancellation_failed';
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

  function planItemLabel(code: string): string {
    const labels: Record<string, MessageKey> = {
      'node.privileged.bootstrap': 'localBootstrapEffectPrivileged',
      'node.data_paths.prepared': 'localBootstrapEffectData',
      'kubernetes.k3s.installed': 'localBootstrapEffectK3S',
      'gitops.argocd.configured': 'localBootstrapEffectArgoCD',
      'dns.dynamic_records.managed': 'localPublicEffectDDNS',
      'certificates.public.issued': 'localPublicEffectCertificates',
      'members.public_ingress.enabled': 'localPublicEffectMemberIngress',
      'headscale.public_coordination.enabled': 'localPublicEffectHeadscale',
      'node.network_ports.changed': 'localBootstrapRiskExposure',
      'node.services.may_restart': 'localBootstrapRiskDowntime',
      'node.atomic_install': 'localBootstrapRiskCancellation',
      'node.data_preserved_on_retry': 'localBootstrapRiskRecovery',
      'router.manual_forwarding': 'localPublicRiskRouter',
      'dns.certificate.propagation_wait': 'localPublicRiskPropagation'
    };
    return labels[code] ? message(labels[code]) : code;
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
  <label class="locale-control" for="launcher-locale">
    <span>{message('language')}</span>
    <select id="launcher-locale" aria-label={message('language')} bind:value={locale} onchange={() => profileLanguage = locale}>
      <option value="en">English</option>
      <option value="de">Deutsch</option>
    </select>
  </label>
</header>

<a class="skip-link" href="#main-content">{locale === 'de' ? 'Zum Inhalt springen' : 'Skip to main content'}</a>

{#if !ready}
  <main id="main-content" class="centered" tabindex="-1"><p role="status">{message('loading')}</p></main>
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

    <main id="main-content" tabindex="-1">
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
          {#if run}<small>{run.currentCheckpoint || message('running')}</small>{/if}
          {#if run?.state === 'running' && run.cancellationState === 'not-requested'}<button class="secondary" onclick={() => void cancelRun()} disabled={busy}>{message('cancel')}</button>{/if}
        </div>

        {#if activeStep === 'capabilities'}
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
            <div class="actions" style="margin-top: 1rem;"><button type="button" onclick={() => activeStep = 'assets'}>{message('continue')}</button></div>
          {/if}
        </section>
        {/if}

        {#if activeStep === 'assets'}
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
            {#if bootstrapAssets.assets.every((asset) => asset.state === 'ready')}
              <div class="actions" style="margin-top: 1rem;"><button type="button" onclick={() => activeStep = activeProfile?.deploymentMode === 'hetzner' ? 'hetzner' : 'node'}>{message('continue')}</button></div>
            {/if}
          {/if}
        </section>
        {/if}

        {#if activeStep === 'node'}
        <section class="card node-card" aria-labelledby="node-title">
          <p class="eyebrow">{message('nodeEyebrow')}</p>
          <h2 id="node-title">{message('nodeTitle')}</h2>
          <p class="muted">{message('nodeDescription')}</p>
          {#if nodeError}<p class="inline-error" role="alert">{nodeError}</p>{/if}
          <form onsubmit={(event) => { event.preventDefault(); void inspectNode(); }}>
            <label><span>{message('nodeTarget')}</span><select bind:value={nodeTargetKind}><option value="remote">{message('nodeRemote')}</option>{#if nodeCapabilities?.sameHostSupported}<option value="same-host">{message('nodeSameHost')}</option>{/if}</select></label>
            {#if activeProfile?.deploymentMode === 'local-lan' || activeProfile?.deploymentMode === 'local-public'}<label><span>{message('localBootstrapDataDirectory')}</span><input bind:value={localBootstrapDataDirectory} required /></label>{/if}
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
          {#if nodeInspection}<dl class="credential-metadata"><div><dt>{message('nodeOperatingSystem')}</dt><dd>{nodeInspection.report.operatingSystem} / {nodeInspection.report.architecture}</dd></div><div><dt>{message('nodeCapacity')}</dt><dd>{formatNumber(locale, nodeInspection.report.capacity.memoryMi)} MiB · {formatNumber(locale, nodeInspection.report.capacity.diskGi)} GiB</dd></div><div><dt>{message('nodeAssessment')}</dt><dd>{nodeInspection.assessment.ready ? message('nodeReady') : nodeInspection.assessment.blockers.map((blocker) => blocker.code).join(', ')}</dd></div></dl>{#if nodeTargetKind === 'remote'}<div class="actions"><button class="secondary" onclick={() => void planNodeSSHKey()} disabled={nodeBusy}>{message('nodePlanSSHKey')}</button></div>{/if}{/if}
          {#if nodeInspection?.assessment.ready && (activeProfile?.deploymentMode === 'local-lan' || activeProfile?.deploymentMode === 'local-public')}
            <section class="capability-preview" aria-labelledby="local-bootstrap-title">
              <p class="eyebrow">{message('localBootstrapEyebrow')}</p>
              <h3 id="local-bootstrap-title">{message('localBootstrapTitle')}</h3>
              <p class="muted">{message('localBootstrapDescription')}</p>
              {#if localBootstrapError}<p class="inline-error" role="alert">{localBootstrapError}</p>{/if}
              <form onsubmit={(event) => { event.preventDefault(); void planLocalBootstrap(); }}>
                <div class="form-grid"><label><span>{message('capabilityDomain')}</span><input bind:value={localBootstrapDomain} required placeholder="home.example" /></label><label><span>{message('localBootstrapEnvironment')}</span><input bind:value={localBootstrapEnvironment} placeholder=".dev" /></label></div>
                <label><span>{message('localBootstrapNodeName')}</span><input bind:value={localBootstrapNodeName} required /></label>
                <label><span>{message('localBootstrapACMEEmail')}</span><input type="email" bind:value={localBootstrapACMEEmail} required={activeProfile?.deploymentMode === 'local-public'} /></label>
                {#if activeProfile?.deploymentMode === 'local-public'}
                  <section class="handoff-steps" aria-labelledby="router-forwarding-title">
                    <h4 id="router-forwarding-title">{message('localPublicRouterTitle')}</h4>
                    <p class="muted">{message('localPublicRouterDescription')}</p>
                    <ul>
                      <li><code>80/tcp → 80/tcp</code> — {message('localPublicRouterHTTP')}</li>
                      <li><code>443/tcp → 443/tcp</code> — {message('localPublicRouterHTTPS')}</li>
                      <li><code>10000/udp → 10000/udp</code> — {message('localPublicRouterJitsi')}</li>
                    </ul>
                    <p class="muted">{message('localPublicRouterNoAutomation')}</p>
                  </section>
                  <label><span>{message('localPublicDNSProvider')}</span><input value="Hetzner DNS (DNS-01)" readonly /></label>
                  <label><span>{message('localPublicDNSToken')}</span><input type="password" bind:value={localPublicDNSToken} required autocomplete="off" /></label>
                  <p class="muted">{message('localPublicDDNS')}</p>
                  <label class="check"><input type="checkbox" bind:checked={localPublicRouterAcknowledged} required /><span>{message('localPublicRouterAcknowledge')}</span></label>
                  <ul><li>{message('localPublicMailWarning')}</li><li>{message('localPublicJitsiWarning')}</li></ul>
                {:else}
                  <label class="check"><input type="checkbox" bind:checked={localBootstrapManageDNS} /><span>{message('localBootstrapManageDNS')}</span></label>
                {/if}
                <label><span>{message('localBootstrapSecrets')}</span><textarea bind:value={localBootstrapSecrets} autocomplete="off" placeholder="apiVersion: v1&#10;kind: Secret&#10;…"></textarea></label>
                {#if nodeTargetKind === 'same-host'}<label><span>{message('nodeSudoPassword')}</span><input type="password" bind:value={nodeSudoPassword} autocomplete="off" /></label>{/if}
                <div class="actions"><button type="submit" disabled={localBootstrapBusy}>{message('localBootstrapReview')}</button></div>
              </form>
              <div class="actions" style="margin-top: 1rem;"><button type="button" onclick={() => activeStep = 'github'}>{message('continue')}</button></div>
            </section>
          {/if}
        </section>
        {/if}

        {#if activeStep === 'github'}
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
          <div class="actions" style="margin-top: 1rem;">
            <button type="button" onclick={() => activeStep = 'generic-git'}>{gitHubStatus ? message('continue') : 'Skip GitHub'}</button>
          </div>
        </section>
        {/if}

        {#if activeStep === 'generic-git'}
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
          <div class="actions" style="margin-top: 1rem;">
            <button type="button" onclick={() => activeStep = 'execute'}>{genericGitStatus ? message('continue') : 'Skip Generic Git'}</button>
          </div>
        </section>
        {/if}

        {#if activeProfile?.deploymentMode === 'hetzner' && activeStep === 'hetzner'}
        <section class="card hetzner-card" aria-labelledby="hetzner-title">
          <p class="eyebrow">{message('hetznerEyebrow')}</p>
          <h2 id="hetzner-title">{message('hetznerTitle')}</h2>
          <p class="muted">{message('hetznerDescription')}</p>
          {#if hetznerError}<p class="inline-error" role="alert">{hetznerError}</p>{/if}

          <form onsubmit={(event) => { event.preventDefault(); void validateHetznerToken(); }}>
            <label><span>{message('hetznerToken')}</span><input type="password" bind:value={hetznerTokenValue} required autocomplete="off" /></label>
            <p class="muted"><a href="https://console.hetzner.cloud/" target="_blank" rel="noreferrer">{message('hetznerTokenGuide')}</a></p>
            <div class="actions"><button type="submit" disabled={hetznerBusy}>{message('hetznerTokenValidate')}</button></div>
          </form>
          {#if hetznerProject?.token}
            <p class="inline-notice" aria-live="polite" data-testid="hetzner-token-verdict">{hetznerTokenLabel(hetznerProject.token.state)}</p>
            {#if hetznerProject.token.fingerprint}
              <dl class="credential-metadata"><div><dt>{message('hetznerTokenFingerprint')}</dt><dd><code>{hetznerProject.token.fingerprint}</code></dd></div>{#if hetznerProject.token.projectId}<div><dt>{message('hetznerProject')}</dt><dd><code>{hetznerProject.token.projectId}</code></dd></div>{/if}</dl>
            {/if}
          {/if}

          {#if hetznerProject?.token?.state === 'valid'}
            <form onsubmit={(event) => { event.preventDefault(); void inspectHetznerProject(); }}>
              <div class="form-grid"><label><span>{message('hetznerDomain')}</span><input bind:value={hetznerDomain} required placeholder="example.org" autocomplete="off" /></label><label><span>{message('hetznerEnvExt')}</span><input bind:value={hetznerEnvExt} placeholder=".dev" autocomplete="off" /></label></div>
              <div class="actions"><button type="submit" disabled={hetznerBusy}>{message('hetznerInspect')}</button></div>
            </form>
          {/if}

          {#if hetznerProject?.inventory}
            <section class="capability-preview" aria-labelledby="hetzner-inventory-title">
              <h3 id="hetzner-inventory-title">{message('hetznerInventory')}</h3>
              <p class="muted">{message('hetznerInspectedAt')}: {formatDateTime(locale, hetznerProject.inspectedAt)}</p>
              <ul class="hetzner-inventory" data-testid="hetzner-inventory">
                {#each hetznerProject.inventory.findings ?? [] as finding (finding.expectation?.kind + '/' + finding.expectation?.name)}
                  <li class:decision={finding.requiresDecision}>
                    <code>{finding.expectation?.kind}</code> <strong>{finding.expectation?.name}</strong>
                    <span class="badge">{hetznerOwnershipLabel(finding.ownership)}</span>
                    {#if finding.match?.detail}<span class="muted">{finding.match.detail}</span>{/if}
                    {#if finding.ownership === 'adoptable' && finding.match?.providerId}
                      <label class="check"><input type="checkbox" checked={hetznerAdoptions.includes(finding.match.providerId)} onchange={() => toggleHetznerAdoption(finding.match?.providerId)} /><span>{message('hetznerAdoptSelected')}</span></label>
                    {/if}
                    {#if (finding.similar ?? []).length > 0}
                      <p class="muted">{message('hetznerSimilarNames')}: {(finding.similar ?? []).map((resource) => resource.name).join(', ')}</p>
                    {/if}
                  </li>
                {/each}
              </ul>
            </section>
          {/if}

          {#if hetznerProject?.delegation}
            <dl class="credential-metadata">
              <div><dt>{message('hetznerDelegation')}</dt><dd data-testid="hetzner-delegation">{hetznerDelegationLabel(hetznerProject.delegation.status)}</dd></div>
              <div><dt>{message('hetznerExpectedNameservers')}</dt><dd>{(hetznerProject.delegation.expectedNameservers ?? []).join(', ')}</dd></div>
              {#if (hetznerProject.delegation.observedNameservers ?? []).length > 0}<div><dt>{message('hetznerObservedNameservers')}</dt><dd>{(hetznerProject.delegation.observedNameservers ?? []).join(', ')}</dd></div>{/if}
            </dl>
          {/if}

          {#if hetznerPresets}
            <section class="capability-preview" aria-labelledby="hetzner-capacity-title">
              <h3 id="hetzner-capacity-title">{message('hetznerCapacity')}</h3>
              <p class="muted">{message('hetznerRequirement')}: {formatNumber(locale, hetznerPresets.requirement?.memoryGb)} GB · {formatNumber(locale, hetznerPresets.requirement?.volumeGb)} GB</p>
              <ul class="hetzner-presets" data-testid="hetzner-presets">
                {#each hetznerPresets.presets ?? [] as preset (preset.tier)}
                  <li>
                    <label class="check"><input type="radio" name="hetzner-preset" value={preset.tier} checked={hetznerTier === preset.tier} onchange={() => { hetznerTier = (preset.tier ?? 'recommended') as HetznerPresetTier; hetznerServerType = preset.serverType ?? ''; hetznerVolumeGb = preset.volumeGb ?? 0; }} /><span><strong>{hetznerPresetLabel(preset.tier)}</strong> · {preset.serverType} · {formatNumber(locale, preset.memoryGb)} GB · {formatNumber(locale, preset.volumeGb)} GB · {formatCurrency(locale, preset.cost?.totalMonthlyEur, preset.cost?.currency ?? 'EUR')}</span></label>
                    {#if preset.fits === false}<p class="muted">{message('hetznerPresetTooSmall')}</p>{/if}
                    {#if preset.available === false}<p class="muted">{message('hetznerPresetUnavailable')}</p>{/if}
                  </li>
                {/each}
              </ul>
              <label class="check"><input type="radio" name="hetzner-preset" value="advanced" checked={hetznerTier === 'advanced'} onchange={() => { hetznerTier = 'advanced'; }} /><span>{message('hetznerAdvancedHint')}</span></label>
              <div class="form-grid">
                <label><span>{message('hetznerLocation')}</span>
                  <select bind:value={hetznerLocation} onchange={() => void loadHetznerPresets()}>
                    {#each hetznerPresets.locations ?? [] as location (location)}<option value={location}>{location}</option>{/each}
                  </select>
                </label>
                {#if hetznerTier === 'advanced'}
                  <label><span>{message('hetznerServerType')}</span>
                    <select bind:value={hetznerServerType}>
                      {#each hetznerPresets.offerings ?? [] as offering (offering.name)}<option value={offering.name}>{offering.name} · {formatNumber(locale, offering.memoryGb)} GB · {formatCurrency(locale, offering.monthlyEur)}</option>{/each}
                    </select>
                  </label>
                  <label><span>{message('hetznerVolume')}</span><input type="number" min="10" max="10240" step="10" bind:value={hetznerVolumeGb} /></label>
                {/if}
              </div>
              <p class="muted">{message('hetznerPricesObservedAt')}: {formatDateTime(locale, hetznerPresets.observedAt)}</p>
              <label><span>{message('hetznerAcmeEmail')}</span><input type="email" bind:value={hetznerAcmeEmail} placeholder="operator@example.org" /></label>
              <p class="muted">{message('hetznerAcmeEmailHint')}</p>
              <div class="actions"><button type="button" onclick={() => void planHetznerInfrastructure()} disabled={hetznerBusy || !hetznerProject?.inventory || !hetznerAcmeEmail.includes('@')}>{message('hetznerPlanBuild')}</button></div>
            </section>
          {/if}

          <section class="capability-preview" aria-labelledby="hetzner-toolchain-title">
            <h3 id="hetzner-toolchain-title">{message('hetznerToolchainTitle')}</h3>
            <p class="muted">{message('hetznerToolchainDescription')}</p>
            {#if hetznerToolchain}
              <dl class="credential-metadata">
                <div><dt>OpenTofu</dt><dd>{hetznerToolchain.openTofuVersion}</dd></div>
                <div><dt>hcloud</dt><dd>{hetznerToolchain.hcloudProviderVersion}</dd></div>
                <div><dt>{message('hetznerToolchainReady')}</dt><dd><span class="badge">{hetznerToolchain.ready ? message('hetznerToolchainReady') : message('hetznerToolchainPending')}</span></dd></div>
              </dl>
              {#if hetznerToolchain.reasonKey === 'toolchain-artifacts-unavailable'}<p class="muted">{message('hetznerToolchainUnavailable')}</p>{/if}
            {/if}
            {#if hetznerWorkspace}
              <dl class="credential-metadata">
                <div><dt>{message('hetznerWorkspace')}</dt><dd>{hetznerWorkspace.isolated ? message('hetznerWorkspaceIsolated') : ''}</dd></div>
                <div><dt>{message('hetznerWorkspaceBackups')}</dt><dd>{formatNumber(locale, hetznerWorkspace.backups ?? 0)}</dd></div>
                {#if hetznerWorkspace.locked}<div><dt>{message('hetznerWorkspaceLocked')}</dt><dd>{hetznerWorkspace.lockOwner}</dd></div>{/if}
              </dl>
            {/if}
            <div class="actions"><button type="button" class="secondary" onclick={() => void acquireHetznerToolchain()} disabled={hetznerBusy}>{message('hetznerToolchainAcquire')}</button></div>
          </section>

          {#if hetznerTemporaryAccess}
            <section class="capability-preview" aria-labelledby="hetzner-access-title" data-testid="hetzner-temporary-access">
              <h3 id="hetzner-access-title">{message('hetznerAccessTitle')}</h3>
              <p class="muted">{message('hetznerAccessDescription')}</p>
              <dl class="credential-metadata">
                <div><dt>{message('hetznerAccessState')}</dt><dd><span class="badge">{hetznerTemporaryAccess.open ? message('hetznerAccessOpen') : message('hetznerAccessClosed')}</span></dd></div>
                <div><dt>{message('hetznerAccessScope')}</dt><dd>{hetznerTemporaryAccess.scope?.scoped ? (hetznerTemporaryAccess.scope?.sources ?? []).join(', ') : message('hetznerAccessUnscoped')}</dd></div>
                <div><dt>{message('hetznerAccessReason')}</dt><dd>{hetznerAccessReasonLabel(hetznerTemporaryAccess.scope?.reasonKey)}</dd></div>
              </dl>
              {#if hetznerTemporaryAccess.open}
                <label><span>{message('hetznerAccessAddress')}</span><input type="text" bind:value={hetznerOperatorAddress} placeholder="198.51.100.7" /></label>
                <p class="muted">{message('hetznerAccessAddressHint')}</p>
                <div class="actions"><button type="button" class="secondary" onclick={() => void narrowHetznerTemporaryAccess()} disabled={hetznerBusy}>{message('hetznerAccessNarrow')}</button></div>
              {/if}
            </section>
          {/if}

          {#if hetznerPlan}
            <section class="capability-preview" aria-labelledby="hetzner-plan-title">
              <p class="eyebrow">{message('planTitle')}</p>
              <h3 id="hetzner-plan-title">{message('hetznerPlanTitle')}</h3>
              <dl class="credential-metadata">
                <div><dt>{message('hetznerServerType')}</dt><dd>{hetznerPlan.changePlan?.choice?.serverType} · {hetznerPlan.changePlan?.choice?.location}</dd></div>
                <div><dt>{message('hetznerVolume')}</dt><dd>{formatNumber(locale, hetznerPlan.changePlan?.choice?.volumeGb)} GB</dd></div>
                <div><dt>{message('hetznerMonthlyCost')}</dt><dd data-testid="hetzner-cost">{hetznerPlanCost(hetznerPlan.changePlan)}</dd></div>
                <div><dt>{message('digest')}</dt><dd><code>{hetznerPlan.changePlan?.digest}</code></dd></div>
              </dl>
              <p class="eyebrow">{message('hetznerPlanItems')}</p>
              <ul class="hetzner-plan-items" data-testid="hetzner-plan-items">
                {#each hetznerPlan.changePlan?.items ?? [] as item (item.kind + '/' + item.name)}
                  <li><span class="badge">{hetznerActionLabel(item.action)}</span> <code>{item.kind}</code> {item.name}</li>
                {/each}
              </ul>
              <ul class="hetzner-cost-notes">
                {#each hetznerPlan.changePlan?.cost?.noteKeys ?? [] as note (note)}<li>{hetznerCostNoteLabel(note)}</li>{/each}
              </ul>
              {#if (hetznerPlan.changePlan?.blockers ?? []).length > 0}
                <p class="eyebrow">{message('hetznerPlanBlockers')}</p>
                <ul class="hetzner-blockers" data-testid="hetzner-blockers">
                  {#each hetznerPlan.changePlan?.blockers ?? [] as blocker (blocker.code + (blocker.name ?? ''))}<li>{hetznerBlockerLabel(blocker.code)} {#if blocker.name}<code>{blocker.name}</code>{/if}</li>{/each}
                </ul>
              {:else}
                <p class="inline-notice">{message('hetznerPlanApprovable')}</p>
                <div class="actions"><button type="button" onclick={() => void approveHetznerPlan()} disabled={hetznerBusy}>{message('hetznerApprove')}</button></div>
              {/if}
            </section>
          {/if}
          <div class="actions" style="margin-top: 1rem;"><button type="button" onclick={() => activeStep = 'github'}>{message('continue')}</button></div>
        </section>
        {/if}

        {#if activeStep === 'execute'}
        <section class="card offsite-card" aria-labelledby="offsite-title">
          <p class="eyebrow">{message('offsiteEyebrow')}</p>
          <h2 id="offsite-title">{message('offsiteTitle')}</h2>
          <p class="muted">{message('offsiteDescription')}</p>
          {#if offsiteError}<p class="inline-error" role="alert">{offsiteError}</p>{/if}
          <form onsubmit={(event) => { event.preventDefault(); void inspectOffsiteDestination(); }}>
            <div class="form-grid"><label><span>{message('offsiteEndpoint')}</span><input type="url" bind:value={offsiteEndpoint} required placeholder="https://s3.eu-central-003.backblazeb2.com" /></label><label><span>{message('offsiteRegion')}</span><input bind:value={offsiteRegion} required placeholder="eu-central-003" autocomplete="off" /></label></div>
            <label><span>{message('offsiteBucket')}</span><input bind:value={offsiteBucket} required placeholder="community-backups" autocomplete="off" /></label>
            <div class="form-grid"><label><span>{message('offsiteAccessKey')}</span><input bind:value={offsiteAccessKey} required autocomplete="off" /></label><label><span>{message('offsiteSecretKey')}</span><input type="password" bind:value={offsiteSecretKey} required autocomplete="off" /></label></div>
            <div class="actions"><button type="submit" disabled={offsiteBusy}>{message('offsiteInspect')}</button></div>
          </form>
          {#if offsiteStatus?.destination?.bucket}
            <dl class="credential-metadata"><div><dt>{message('offsiteBucket')}</dt><dd>{offsiteStatus.destination.bucket} · {offsiteStatus.destination.region}</dd></div><div><dt>{message('offsiteVersioning')}</dt><dd>{offsiteVersioningLabel(offsiteStatus.versioning)}</dd></div><div><dt>{message('offsiteFingerprint')}</dt><dd><code>{offsiteStatus.accessKeyFingerprint}</code></dd></div></dl>
            {#if offsiteStatus.requiresAcknowledgement}<label class="check"><input type="checkbox" bind:checked={offsiteAcknowledge} /><span>{message('offsiteAcknowledge')}</span></label>{/if}
            <div class="actions"><button type="button" onclick={() => void planOffsiteProtection()} disabled={offsiteBusy || (offsiteStatus.requiresAcknowledgement && !offsiteAcknowledge)}>{message('offsitePlanReview')}</button></div>
          {/if}
          {#if offsitePlan}
            <section class="capability-preview" aria-labelledby="offsite-plan-title">
              <p class="eyebrow">{message('planTitle')}</p>
              <h3 id="offsite-plan-title">{message('offsiteGitDiff')}</h3>
              <div data-testid="offsite-diff" class="overlay-diff" role="textbox" aria-readonly="true" tabindex="0" aria-label={message('offsiteGitDiff')}>{offsitePlan.gitDiff}</div>
              <dl class="credential-metadata"><div><dt>{message('offsiteSecretEffect')}</dt><dd><code>{offsitePlan.secret?.secretName}</code></dd></div><div><dt>{message('offsiteSecretKeysLabel')}</dt><dd>{(offsitePlan.secret?.keys ?? []).join(', ')}</dd></div></dl>
              <p class="eyebrow">{message('offsiteImplications')}</p>
              <ul class="offsite-implications"><li>{offsiteImplicationLabel(offsitePlan.implications?.data)}</li><li>{offsiteImplicationLabel(offsitePlan.implications?.cost)}</li><li>{offsiteImplicationLabel(offsitePlan.implications?.protection)}</li></ul>
              <div class="actions"><button type="button" onclick={() => void proposeOffsiteProtection()} disabled={offsiteBusy || !offsitePlanId}>{message('offsiteApprovePropose')}</button></div>
            </section>
          {/if}
          {#if offsiteProposal}<p class="inline-notice" aria-live="polite">{message('offsiteProposalOpened')} {#if offsiteProposal.url}<a href={offsiteProposal.url} target="_blank" rel="noreferrer">{offsiteProposal.branch || offsiteProposal.commit}</a>{:else}<code>{offsiteProposal.branch}</code> · {offsiteProposal.commit}{/if}</p>{/if}
          {#if offsiteStatus?.proposal}
            <p class="muted">{message('offsiteProposalRequired')}</p>
            <div class="actions"><button type="button" class="secondary" onclick={() => void validateOffsiteProtection()} disabled={offsiteBusy}>{message('offsiteValidate')}</button></div>
          {/if}
          {#if offsiteStatus?.validation}
            <dl class="credential-metadata"><div><dt>{message('offsiteValidationVerdict')}</dt><dd>{offsiteResultLabel(offsiteStatus.validation.result)}</dd></div><div><dt>{message('offsiteRemediation')}</dt><dd>{offsiteRemediationLabel(offsiteStatus.validation.remediationKey)}</dd></div>{#if offsiteStatus.validation.recoveryPointAt}<div><dt>{message('offsiteRecoveryPoint')}</dt><dd>{formatDateTime(locale, offsiteStatus.validation.recoveryPointAt)}</dd></div>{/if}</dl>
          {/if}
        </section>

        <section class="card decommission-card" aria-labelledby="decommission-title">
          <p class="eyebrow">{decommissionMessage('eyebrow')}</p>
          <h2 id="decommission-title">{decommissionMessage('preserveTitle')}</h2>
          <p class="muted">{decommissionMessage('preserveDescription')}</p>
          {#if decommissionError}<p class="inline-error" role="alert">{decommissionError}</p>{/if}
          {#if !decommissionPlan}
            <button type="button" class="danger" onclick={() => void planPreserveDataDecommission()} disabled={decommissionBusy}>{decommissionMessage('preservePlan')}</button>
          {:else}
            <dl>
              <div><dt>{decommissionMessage('planDigest')}</dt><dd><code>{decommissionPlan.decommission.digest}</code></dd></div>
              <div><dt>{decommissionMessage('expectedDowntime')}</dt><dd>{decommissionPlan.decommission.expectedDowntime}</dd></div>
              <div><dt>{decommissionMessage('recoveryPath')}</dt><dd>{decommissionPlan.decommission.recoveryPath}</dd></div>
              <div><dt>{decommissionMessage('retainedData')}</dt><dd>{decommissionPlan.decommission.retainedData.join(', ') || decommissionMessage('noneDeclared')}</dd></div>
              <div><dt>{decommissionMessage('continuingCost')}</dt><dd>{decommissionPlan.decommission.continuingCosts.map((cost) => `${cost.kind} ${formatCurrency(locale, cost.monthlyEur)}`).join('; ') || decommissionMessage('none')}</dd></div>
            </dl>
            {#if decommissionPlan.decommission.blockers?.length}
              <p class="inline-error"><strong>{decommissionMessage('deletionBlocked')}</strong> {decommissionPlan.decommission.blockers.join('; ')}</p>
            {/if}
            <ul class="handoff-checklist">
              {#each decommissionPlan.decommission.items as item (item.providerId)}
                <li><span aria-hidden="true">{item.action === 'remove' ? '!' : '✓'}</span> <code>{item.kind}/{item.providerId}</code> — {item.action} ({item.ownership})</li>
              {/each}
            </ul>
            <div class="actions">
              <button type="button" class="secondary" onclick={() => void planPreserveDataDecommission()} disabled={decommissionBusy}>{decommissionMessage('reinspectPlan')}</button>
              <button type="button" class="danger" onclick={() => void approvePreserveDataDecommission()} disabled={decommissionBusy || !decommissionPlan.approvable}>{decommissionMessage('approvePreserve')}</button>
            </div>
            {#if decommissionRun}
              <p class="inline-notice">{decommissionMessage('run')}: <code>{decommissionRun.id}</code> — {runLabel(decommissionRun.state)} {decommissionMessage('at')} {decommissionRun.currentCheckpoint}</p>
              {#if decommissionRun.state === 'running' && decommissionRun.currentCheckpoint === 'interrupted'}
                <button type="button" onclick={() => void resumePreserveDataDecommission()} disabled={decommissionBusy}>{decommissionMessage('resume')}</button>
              {/if}
            {/if}
          {/if}
          <hr />
          <p class="muted">{decommissionMessage('forgetDescription')}</p>
          <button type="button" class="secondary" onclick={() => void forgetActiveProfile()} disabled={decommissionBusy}>{decommissionMessage('forget')}</button>
        </section>

        <section class="card decommission-card full-decommission-card" aria-labelledby="full-decommission-title">
          <p class="eyebrow">{decommissionMessage('irreversible')}</p>
          <h2 id="full-decommission-title">{decommissionMessage('fullTitle')}</h2>
          <p class="muted">{decommissionMessage('fullDescription')}</p>
          {#if fullDecommissionError}<p class="inline-error" role="alert">{fullDecommissionError}</p>{/if}
          {#if !fullDecommissionPlan}
            <button type="button" class="danger" onclick={() => void planFullDecommission()} disabled={fullDecommissionBusy}>{decommissionMessage('fullPlan')}</button>
          {:else}
            <dl>
              <div><dt>{decommissionMessage('planDigest')}</dt><dd><code>{fullDecommissionPlan.decommission.digest}</code></dd></div>
              <div><dt>{decommissionMessage('backupFreshness')}</dt><dd>{fullDecommissionPlan.decommission.protection.backupFreshness}</dd></div>
              <div><dt>{decommissionMessage('offsitePoints')}</dt><dd>{fullDecommissionPlan.decommission.protection.offsiteRecoveryPoints.join(', ') || decommissionMessage('noneObserved')}</dd></div>
              <div><dt>{decommissionMessage('recoveryBundle')}</dt><dd>{fullDecommissionPlan.decommission.protection.recoveryBundleStatus}</dd></div>
            </dl>
            {#if fullDecommissionPlan.decommission.requiresOwnerOverride}
              <div class="inline-error" role="alert">
                <strong>{decommissionMessage('protectionInsufficient')}</strong> {fullDecommissionPlan.decommission.protection.warnings?.join('; ') || decommissionMessage('protectionMissing')}
                <label class="check"><input type="checkbox" bind:checked={fullDecommissionOverride} /><span>{decommissionMessage('overrideAccept')}</span></label>
                <label>{decommissionMessage('overrideReason')} <input bind:value={fullDecommissionOverrideReason} maxlength="500" /></label>
              </div>
            {/if}
            <p class="muted">{decommissionMessage('irreversibleConsequences')}</p>
            <ul class="handoff-checklist">
              {#each fullDecommissionPlan.decommission.irreversibleConsequences as consequence}
                <li><span aria-hidden="true">!</span> {consequence}</li>
              {/each}
            </ul>
            <ul class="handoff-checklist">
              {#each fullDecommissionPlan.decommission.items as item (item.providerId)}
                <li><span aria-hidden="true">{item.action === 'remove' ? '!' : '✓'}</span> <code>{item.kind}/{item.providerId}</code> — {item.action} {#if item.stage}({item.stage}){/if}; {item.consequence}</li>
              {/each}
            </ul>
            <label>Type this exact confirmation to authorize irreversible deletion
              <code class="typed-confirmation">{fullDecommissionPlan.decommission.typedConfirmation}</code>
              <input bind:value={fullDecommissionConfirmation} autocomplete="off" spellcheck="false" aria-label={decommissionMessage('typedConfirmation')} />
            </label>
            <div class="actions">
              <button type="button" class="secondary" onclick={() => void planFullDecommission()} disabled={fullDecommissionBusy}>{decommissionMessage('discardReinspect')}</button>
              <button type="button" class="danger" onclick={() => void approveFullDecommission()} disabled={fullDecommissionBusy || fullDecommissionConfirmation !== fullDecommissionPlan.decommission.typedConfirmation || (fullDecommissionPlan.decommission.requiresOwnerOverride && (!fullDecommissionOverride || !fullDecommissionOverrideReason.trim()))}>{decommissionMessage('typeConfirm')}</button>
            </div>
            {#if fullDecommissionRun}
              <p class="inline-notice">{decommissionMessage('run')}: <code>{fullDecommissionRun.id}</code> — {runLabel(fullDecommissionRun.state)} {decommissionMessage('at')} {fullDecommissionRun.currentCheckpoint}</p>
              {#if fullDecommissionRun.state === 'running' && fullDecommissionRun.currentCheckpoint.includes('interrupted')}
                <button type="button" onclick={() => void resumeFullDecommission()} disabled={fullDecommissionBusy}>{decommissionMessage('resumeScope')}</button>
              {/if}
              {#if fullDecommissionRun.state === 'verified'}
                <button type="button" class="secondary" onclick={exportFullDecommissionActivity}>{decommissionMessage('exportRecord')}</button>
              {/if}
            {/if}
          {/if}
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

        {#if activeProfile?.deploymentMode === 'local-lan' || activeProfile?.deploymentMode === 'local-public'}
        <section class="card handoff-card" aria-labelledby="handoff-title">
          <p class="eyebrow">{message('handoffEyebrow')}</p>
          <h2 id="handoff-title">{activeProfile.deploymentMode === 'local-public' ? message('localPublicHandoffTitle') : message('handoffTitle')}</h2>
          <p class="muted">{activeProfile.deploymentMode === 'local-public' ? message('localPublicHandoffDescription') : message('handoffDescription')}</p>
          {#if handoffError}<p class="inline-error" role="alert">{handoffError}</p>{/if}
          {#if handoffAssessment}
            <section class="handoff-steps" aria-label={message('handoffStepsTitle')}>
              <h3>{message('handoffStepsTitle')}</h3>
              <ul class="handoff-checklist">
                {#each handoffAssessment.steps as step (step.name)}
                  <li class:complete={step.complete}><span aria-hidden="true">{step.complete ? '✓' : '○'}</span> {handoffStepLabel(step.name)}</li>
                {/each}
              </ul>
            </section>
          {/if}
          {#if vaultStatus?.state === 'unlocked'}
            {#if activeProfile.deploymentMode === 'local-lan'}
              <div class="actions"><button type="button" onclick={() => void establishClusterCA()} disabled={handoffBusy}>{message('handoffClusterCAEstablish')}</button><button type="button" class="secondary" onclick={() => void installDeviceTrust()} disabled={handoffBusy}>{message('handoffDeviceTrustInstall')}</button></div>
              {#if deviceTrustFingerprint}<p class="inline-notice">{message('handoffDeviceTrustFingerprint')}: <code>{deviceTrustFingerprint}</code></p>{/if}
            {/if}
            <form onsubmit={(event) => { event.preventDefault(); void establishPrivateNetwork(); }}>
              <label><span>{message('handoffBaseDomain')}</span><input bind:value={handoffBaseDomain} required placeholder="smallworlds.internal" /></label>
              <div class="actions"><button type="submit" disabled={handoffBusy}>{message('handoffPrivateNetworkEstablish')}</button></div>
            </form>
            <div class="actions"><button type="button" onclick={() => void detectTailscale()} disabled={handoffBusy}>{message('handoffTailscaleDetect')}</button></div>
            {#if tailscaleOffer}
              <p class="inline-notice">{tailscaleOffer.detected ? message('handoffTailscaleDetected') : message('handoffTailscaleAbsent')} {#if tailscaleOffer.acquisition.available}{message('handoffTailscaleAcquire')} {/if}<a href={tailscaleOffer.acquisition.manualInstructionsUrl} target="_blank" rel="noreferrer">{message('handoffTailscaleManual')}</a></p>
            {/if}
            <div class="actions"><button type="button" onclick={() => void establishEnrollment()} disabled={handoffBusy}>{message('handoffEnrollmentEstablish')}</button><button type="button" class="secondary" onclick={() => void consumeLauncherEnrollment()} disabled={handoffBusy}>{message('handoffLauncherConsume')}</button></div>
            <div class="actions"><button type="button" onclick={() => void verifyHandoff()} disabled={handoffBusy}>{message('handoffVerify')}</button><button type="button" class="secondary" onclick={() => void closeTemporaryAccess()} disabled={handoffBusy}>{message('handoffCloseAccess')}</button></div>
            <div class="actions"><button type="button" onclick={() => void claimFirstOwner()} disabled={handoffBusy}>{message('handoffFirstOwnerClaim')}</button><button type="button" onclick={() => void registerFirstOwner()} disabled={handoffBusy || !firstOwnerChallenge}>{message('handoffFirstOwnerRegister')}</button></div>
          {:else}
            <p class="muted">{message('handoffUnlockFirst')}</p>
          {/if}
          {#if handoffAssessment}
            <section class="handoff-limitations" aria-label={message('handoffLimitations')}>
              <h3>{message('handoffLimitations')}</h3>
              <ul>{#each handoffAssessment.limitations as limitation (limitation)}<li>{limitation}</li>{/each}</ul>
            </section>
            {#if handoffAssessment.complete && handoffAssessment.consoleHandoffUrl}
              <p class="inline-notice" data-testid="console-handoff-url">{message('handoffConsoleUrl')}: <a href={handoffAssessment.consoleHandoffUrl}>{handoffAssessment.consoleHandoffUrl}</a></p>
            {/if}
          {/if}
        </section>
        {/if}

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
            <p class="eyebrow">{message('capabilityPreview')}</p>
            <h2 id="plan-title">{message('planTitle')}</h2>
            <dl>
              <div><dt>{message('digest')}</dt><dd data-testid="plan-digest"><code>{plan.digest}</code></dd></div>
              <div><dt>{message('effect')}</dt><dd>{plan.effects?.map((entry) => planItemLabel(entry.code)).join('; ') || message('effect')}</dd></div>
              <div><dt>{message('noRisk')}</dt><dd>{plan.risks?.map((entry) => planItemLabel(entry.code)).join('; ') || message('noRisk')}</dd></div>
              {#if plan.preconditions.bootstrapRelease}<div><dt>{message('capabilityRelease')}</dt><dd>{plan.preconditions.bootstrapRelease}</dd></div>{/if}
              {#if plan.preconditions.overlayCommit}<div><dt>{message('localBootstrapOverlayCommit')}</dt><dd><code>{plan.preconditions.overlayCommit}</code></dd></div>{/if}
              {#if plan.preconditions.dataDirectory}<div><dt>{message('localBootstrapDataDirectory')}</dt><dd><code>{plan.preconditions.dataDirectory}</code></dd></div>{/if}
            </dl>
            <div class="actions">
              <button onclick={() => void approvePlan()} disabled={busy || run?.state === 'running'}>{message('approve')}</button>
            </div>
          </section>
        {/if}
        {/if}

        <section aria-labelledby="activity-title">
          <p class="eyebrow">{message('activity')}</p>
          <h2 id="activity-title">{message('activity')}</h2>
          {#if activities.length === 0}
            <p class="muted">—</p>
          {:else}
            <ol class="timeline">
              {#each activities as activity (activity.id)}
                <li><span aria-hidden="true"></span><code>{activity.type}</code><time datetime={activity.occurredAt}>{formatDateTime(locale, activity.occurredAt)}</time></li>
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
  .skip-link { position: fixed; z-index: 10; left: 1rem; top: -5rem; padding: .7rem 1rem; border-radius: .6rem; background: #ef9f27; color: #17211b; font-weight: 800; }
  .skip-link:focus { top: 1rem; }
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
	.decommission-card { margin: 0 0 2rem; border-left: 5px solid #b5473b; }
	.decommission-card hr { border: 0; border-top: 1px solid #dce5de; margin: 1.5rem 0; }
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
  .timeline time { margin-left: auto; color: #5f6c64; font-size: .85rem; }
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
    .actions { justify-content: stretch; flex-wrap: wrap; }
    .actions button { flex: 1 1 12rem; min-height: 2.8rem; }
    .run-status { align-items: flex-start; flex-wrap: wrap; }
    .run-status small { width: 100%; margin-left: 0; }
    .timeline li { align-items: flex-start; flex-wrap: wrap; }
    .timeline time { width: 100%; margin-left: 1.4rem; }
  }
  @media (prefers-color-scheme: dark) {
    :global(:root) { color: #edf6ef; background: #122019; }
    .product-header { background: #08150e; border-color: #3a6550; }
    aside { background: #172b20; border-color: #3a6550; }
    nav button, nav button small, .muted, .eyebrow, .run-status small, .timeline time { color: inherit; }
    nav button:hover, nav button.active, .card { background: #1b3024; border-color: #466b55; }
    input, select, textarea { background: #102017; color: #edf6ef; border-color: #7d9c87; }
    button.secondary { color: #edf6ef; border-color: #9ab9a4; }
    button.secondary:hover { background: #284433; }
    .run-status { background: #263b2e; }
    .run-status.verified, .badge, .inline-notice { background: #204a32; color: #e2f8e8; }
    .inline-error, .error { background: #4a2622; color: #ffe9e4; }
  }
  @media (prefers-reduced-motion: reduce) { :global(*) { scroll-behavior: auto !important; transition: none !important; animation: none !important; } }
  @media (prefers-contrast: more) { :global(:root) { background: white; color: black; } .card, input, select, button.secondary { border-width: 2px; border-color: currentColor; } }
</style>
