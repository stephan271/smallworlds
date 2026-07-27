<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api, initializeSession, type BootstrapAssetRequirements, type CapabilityCatalog, type CapabilityMode, type CapabilityPlanResult, type ChangePlan, type ClusterProfile, type CredentialMetadata, type FullDecommissionResult, type GenericGitCredentialStatus, type GenericGitProposal, type GitHubTokenStatus, type HandoffAssessment, type HetznerChangePlan, type HetznerPlanResult, type HetznerPresets, type HetznerPresetTier, type HetznerProject, type HetznerToolchain, type HetznerWorkspace, type TemporaryAccess, type NodeCapabilities, type NodeInspectionResult, type NodeProbeResult, type NodeTarget, type NodeTrust, type OffsitePlan, type OverlayIdentity, type OffsiteProposal, type OffsiteProtection, type PreserveDataDecommissionResult, type RecoveryBundlePreview, type SetupSettings, type TailscaleClientOffer, type VaultStatus, type WorkflowRun } from '$lib/api';
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

  /** The three places this console has. Setting an installation up is a finite
   *  job with an order; looking after the one you have is not. Keeping them
   *  apart means a stage never has to be scrolled past to reach either. */
  type TabId = 'setup' | 'activity' | 'manage';

  /** One stage of setting a cluster up, in the order of the Setup Journey.
   *  Which of these exist at all depends on where the cluster runs, so an
   *  operator is never shown a rented-server stage for a machine in their own
   *  building. The order is load-bearing rather than cosmetic: the settings
   *  repository has to exist before a server is touched, because installing is
   *  precisely the act of pointing the new cluster at it. */
  type StepId = 'design' | 'settings-repo' | 'machine' | 'install' | 'handoff' | 'protect' | 'finish';

  type Step = {
    id: StepId;
    titleKey: MessageKey;
    summaryKey: MessageKey;
    /** True once the launcher has observed this stage finished — never merely
     *  because the operator clicked past it. */
    done: boolean;
    /** Why this stage cannot be worked on yet, or '' when it can. */
    blockedKey: MessageKey | '';
  };

  let locale: Locale = $state('en');
  let ready = $state(false);
  let error = $state('');
  let profiles: ClusterProfile[] = $state([]);
  let activeProfile: ClusterProfile | null = $state(null);
  let run: WorkflowRun | null = $state(null);
  /** What the current run is doing. Without it a verified self-test would be
   *  indistinguishable from a verified installation, and the journey would
   *  claim a cluster exists because a rehearsal succeeded. */
  let runPurpose: 'install' | 'self-test' | 'retire' | '' = $state('');
  let activities: ActivityEvent[] = $state([]);
  let vaultStatus: VaultStatus | null = $state(null);
  let credentials: CredentialMetadata[] = $state([]);
  let vaultError = $state('');
  let vaultBusy = $state(false);
  let vaultPassphrase = $state('');
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
  let capabilityPlan: CapabilityPlanResult | null = $state(null);
  let capabilityError = $state('');
  let capabilityBusy = $state(false);

  // --- One answer, one place ------------------------------------------------
  // These used to exist three times over — once for the overlay, once for the
  // installation, once for Hetzner — and an operator who typed the same domain
  // twice with a typo discovered it as a refusal twenty minutes later. They are
  // asked for exactly once, in the stage they belong to, and every later stage
  // reads them from here.
  let release = $state('v1.2.27');
  let domain = $state('');
  let environmentExtension = $state('');
  let adminEmail = $state('');
  const environmentExtensionPattern = '(\\.[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?)?';

  let settingsProvider: 'github' | 'generic' | '' = $state('');
  let capabilityRepositoryURL = $state('');
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
  // Recorded in earlier sessions rather than observed in this one: which machine
  // this profile pinned, and which settings repository it deploys from.
  let nodeTrust: NodeTrust | null = $state(null);
  let overlayIdentity: OverlayIdentity | null = $state(null);
  let nodeError = $state('');
  let nodeBusy = $state(false);
  let cleanNodeBusy = $state(false);
  let sshKeyPlan: ChangePlan | null = $state(null);
  let localBootstrapDataDirectory = $state('/var/lib/smallworlds-data');
  let localBootstrapNodeName = $state('smallworlds-local-node');
  let localBootstrapManageDNS = $state(false);
  let localPublicDNSToken = $state('');
  let localPublicRouterAcknowledged = $state(false);
  let localBootstrapSecrets = $state('');
  let localBootstrapError = $state('');
  let localBootstrapBusy = $state(false);
  /** The change plan for building the cluster, whichever mode builds it. */
  let installPlan: ChangePlan | null = $state(null);

  let handoffAssessment: HandoffAssessment | null = $state(null);
  let handoffBaseDomain = $state('smallworlds.internal');
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
  let hetznerPresets: HetznerPresets | null = $state(null);
  let hetznerTier: HetznerPresetTier = $state('recommended');
  let hetznerLocation = $state('');
  let hetznerServerType = $state('');
  let hetznerVolumeGb = $state(0);
  let hetznerAdoptions: string[] = $state([]);
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

  // The rehearsal. It answers "is this console working at all", which is a
  // question an operator only ever asks when something else already went wrong,
  // so it lives with the diagnostics rather than in the middle of the journey.
  let selfTestPlan: ChangePlan | null = $state(null);
  let selfTestBusy = $state(false);

  let activeTab: TabId = $state('setup');
  let activeStep: StepId = $state('design');
  let showRecovery = $state(false);
  let showRetire = $state(false);
  let advancedDesign = $state(false);
  let advancedMachine = $state(false);
  let advancedInstall = $state(false);
  let creating = $state(true);
  let editing = $state(false);
  let busy = $state(false);
  let profileName = $state('');
  let profileLanguage: Locale = $state('en');
  let deploymentMode: 'hetzner' | 'local-lan' | 'local-public' = $state('local-lan');
  let eventSource: EventSource | null = null;
  let pollTimer: number | undefined;
  let settingsSaveTimer: number | undefined;
  // Suppresses the autosave while applySettings() is writing the loaded answers
  // back into the bound fields, which would otherwise echo them straight back.
  let hydrating = false;

  const message = (key: MessageKey) => translate(locale, key);
  const decommissionMessage = (key: Parameters<typeof decommissionCopy>[1]) => decommissionCopy(locale, key);

  // Written as $derived.by rather than $derived throughout: a bare $derived
  // expression sits in the same control flow as the declarations above it, so
  // TypeScript still believes every one of those is its initial value. Reading
  // them inside a closure is what makes the check see the real types.
  const rentsMachine = $derived.by(() => activeProfile?.deploymentMode === 'hetzner');

  // --- Guided steps --------------------------------------------------------
  // The journey is derived from what the launcher has actually observed, not
  // from a counter the browser increments. A stage is "done" only on evidence,
  // and a stage the operator cannot usefully work on yet says why rather than
  // silently disappearing — hiding it would leave them unable to tell whether
  // the step exists at all.

  /** Evidence recorded in an earlier session counts just as much as evidence
   *  observed in this one — an established overlay proves the capabilities were
   *  chosen and the repository written, however long ago the browser was closed. */
  const designChosen = $derived.by(() => capabilityPlan !== null || overlayIdentity !== null);
  /** Not "a token was accepted": the repository has to exist and hold the
   *  configuration, because that is what the installation is pointed at. */
  const repositoryEstablished = $derived.by(() => overlayIdentity !== null);
  /** A pinned host key is deliberately not enough here: it proves which machine
   *  this is, not that the machine is still suitable to install onto. That
   *  verdict is only ever a fresh inspection's to give. */
  const machineReady = $derived.by(() => rentsMachine ? hetznerPlan !== null : nodeInspection?.assessment.ready === true);
  const installerFilesReady = $derived.by(() => bootstrapAssets !== null && bootstrapAssets.assets.every((asset) => asset.state === 'ready'));
  const installObserved = $derived.by(() => runPurpose === 'install' && run?.state === 'verified');
  const handoffComplete = $derived.by(() => handoffAssessment?.complete === true);
  const protectionVerified = $derived.by(() => offsiteStatus?.validation?.result === 'offsite-verified');
  /** Whether there is anything out there to shut down. Offering to decommission
   *  an installation that was never built is an invitation to worry about a
   *  machine that does not exist. */
  const clusterEstablished = $derived.by(() => repositoryEstablished && (nodeTrust !== null || hetznerProject?.inventory != null));

  const steps: Step[] = $derived.by(() => {
    const list: Step[] = [
      { id: 'design', titleKey: 'stepCapabilitiesTitle', summaryKey: 'stepCapabilitiesSummary', done: designChosen, blockedKey: '' },
      { id: 'settings-repo', titleKey: 'stepSettingsRepoTitle', summaryKey: 'stepSettingsRepoSummary', done: repositoryEstablished, blockedKey: designChosen ? '' : 'stepBlockedChooseFirst' },
      rentsMachine
        ? { id: 'machine', titleKey: 'stepHetznerTitle', summaryKey: 'stepHetznerSummary', done: machineReady, blockedKey: repositoryEstablished ? '' : 'stepBlockedRepositoryFirst' }
        : { id: 'machine', titleKey: 'stepNodeTitle', summaryKey: 'stepNodeSummary', done: machineReady, blockedKey: repositoryEstablished ? '' : 'stepBlockedRepositoryFirst' },
      { id: 'install', titleKey: 'stepInstallTitle', summaryKey: 'stepInstallSummary', done: installObserved, blockedKey: machineReady ? '' : 'stepBlockedMachineFirst' },
      { id: 'handoff', titleKey: 'stepHandoffTitle', summaryKey: 'stepHandoffSummary', done: handoffComplete, blockedKey: installObserved ? '' : 'stepBlockedInstallFirst' },
      { id: 'protect', titleKey: 'stepProtectTitle', summaryKey: 'stepProtectSummary', done: protectionVerified, blockedKey: installObserved ? '' : 'stepBlockedInstallFirst' },
      { id: 'finish', titleKey: 'stepFinishTitle', summaryKey: 'stepFinishSummary', done: handoffComplete && protectionVerified, blockedKey: installObserved ? '' : 'stepBlockedInstallFirst' }
    ];
    return list;
  });

  const currentStep: Step | undefined = $derived(steps.find((step) => step.id === activeStep));
  const currentStepIndex = $derived(steps.findIndex((step) => step.id === activeStep));
  /** The one thing worth doing next. Everything else on the rail is context. */
  const nextStep: Step | undefined = $derived(steps.find((step) => !step.done && !step.blockedKey));

  function goToStep(id: StepId): void {
    activeStep = id;
    activeTab = 'setup';
    void prepareStep(id);
  }

  function goToPreviousStep(): void {
    const previous = steps[currentStepIndex - 1];
    if (previous) goToStep(previous.id);
  }

  function goToNextStep(): void {
    const following = steps[currentStepIndex + 1];
    if (following) goToStep(following.id);
  }

  /** After loading a profile, open the stage the operator actually has work on
   *  rather than always dropping them back at the first one. */
  function openFirstOpenStep(): void {
    const target = nextStep ?? steps[steps.length - 1] ?? steps[0];
    if (target) goToStep(target.id);
  }

  // --- Fetching rather than asking -----------------------------------------
  // Everything below is work the launcher can do without a decision from the
  // operator. Asking them to press a button for it would be asking them to
  // confirm something they have no basis to answer differently, so the stage
  // simply does it and reports what happened.

  async function prepareStep(id: StepId): Promise<void> {
    if (!activeProfile) return;
    if (id === 'settings-repo') await ensureCapabilityPlan();
    if (id === 'machine' && rentsMachine) await ensureHetznerToolchain();
    if (id === 'machine' || id === 'install') await ensureInstallerFiles();
    if (id === 'handoff') await ensureTailscaleOffer();
  }

  /** The exact file listing an operator approves before the repository is
   *  written. It is rebuilt rather than remembered, because the answers behind
   *  it may have changed since it was last rendered. */
  async function ensureCapabilityPlan(): Promise<void> {
    if (!activeProfile || capabilityBusy || overlayIdentity) return;
    if (!domain || !release) return;
    if (settingsProvider === 'generic' && !capabilityRepositoryURL) return;
    if (settingsProvider === '') return;
    await planCapabilities();
  }

  async function ensureInstallerFiles(): Promise<void> {
    if (bootstrapAssetBusy || !release) return;
    if (bootstrapAssets?.release === release && installerFilesReady) return;
    bootstrapAssetBusy = true;
    bootstrapAssetError = '';
    try {
      const required = await api.getBootstrapAssetRequirements(release);
      bootstrapAssets = required.assets.every((asset) => asset.state === 'ready')
        ? required
        : await api.acquireBootstrapAssets(release);
    } catch (reason) {
      bootstrapAssets = null;
      bootstrapAssetError = reason instanceof Error ? reason.message : 'bootstrap_asset_acquisition_failed';
    } finally {
      bootstrapAssetBusy = false;
    }
  }

  async function ensureHetznerToolchain(): Promise<void> {
    if (!activeProfile || hetznerBusy || hetznerToolchain?.ready) return;
    await acquireHetznerToolchain();
  }

  async function ensureTailscaleOffer(): Promise<void> {
    if (tailscaleOffer) return;
    try {
      tailscaleOffer = await api.getTailscaleClient();
    } catch {
      tailscaleOffer = null;
    }
  }

  /** The safe is opened with this computer's own login where that exists, which
   *  is what the operator would have chosen anyway. Only a machine without one —
   *  or a vault that predates one — ever sees a prompt. */
  async function ensureVaultOpen(): Promise<void> {
    if (vaultStatus?.state === 'unlocked') return;
    if (!vaultStatus?.osCredentialStoreAvailable) return;
    try {
      vaultStatus = await api.unlockVault('operating-system');
    } catch {
      // A vault created with a passphrase has no wrapping key in the store, and
      // a store can be present but refuse to answer. Either way the operator is
      // asked, which is the only remaining honest option.
    }
  }

  $effect(() => {
    document.documentElement.lang = locale;
  });

  onMount(async () => {
    try {
      await initializeSession();
      [profiles, vaultStatus, capabilityCatalog, nodeCapabilities] = await Promise.all([api.listProfiles(), api.getVaultStatus(), api.getCapabilities(), api.getNodeCapabilities()]);
      await ensureVaultOpen();
      try {
        const res = await fetch('https://api.github.com/repos/stephan271/smallworlds/releases/latest');
        if (res.ok) {
          const data = await res.json();
          if (data.tag_name) release = data.tag_name;
        }
      } catch {
        // Fallback to the built-in default when this computer is offline.
      }
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
    try {
      applySettings(await api.getSettings(profile.id));
    } catch {
      // A profile with no saved answers yet simply keeps the defaults.
    }
    credentials = vaultStatus?.state === 'unlocked' ? await api.listCredentials(profile.id) : [];
    // Everything this browser session observed belongs to the profile it was
    // observed for. Carrying it into another profile would claim its stages were
    // done on evidence about a different cluster.
    capabilityPlan = null;
    capabilityError = '';
    bootstrapAssets = null;
    bootstrapAssetError = '';
    nodeProbe = null;
    nodeInspection = null;
    nodeError = '';
    sshKeyPlan = null;
    gitHubStatus = null;
    gitHubError = '';
    gitHubOverlayNotice = '';
    genericGitStatus = null;
    genericGitError = '';
    genericGitProposal = null;
    installPlan = null;
    selfTestPlan = null;
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
    showRetire = false;
    try {
      offsiteStatus = await api.getOffsiteProtection(profile.id);
    } catch {
      offsiteStatus = null;
    }
    // A recorded machine is authoritative over the typed answer: it is the one
    // whose host key was actually confirmed. Fall back to the saved answer so a
    // profile that has not got that far keeps what the operator already typed.
    try {
      nodeTrust = await api.getNodeTrust(profile.id);
      nodeHost = nodeTrust.host || nodeHost;
      nodePort = nodeTrust.port || nodePort;
      nodeUsername = nodeTrust.username || nodeUsername;
    } catch {
      nodeTrust = null;
    }
    try {
      overlayIdentity = await api.getOverlayIdentity(profile.id);
      capabilityRepositoryURL = overlayIdentity.repositoryUrl || capabilityRepositoryURL;
      release = overlayIdentity.release || release;
      domain = overlayIdentity.domain || domain;
    } catch {
      overlayIdentity = null;
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
    decommissionPlan = null;
    decommissionRun = null;
    decommissionError = '';
    fullDecommissionPlan = null;
    fullDecommissionRun = null;
    fullDecommissionConfirmation = '';
    fullDecommissionOverride = false;
    fullDecommissionOverrideReason = '';
    fullDecommissionError = '';
    if (profile.deploymentMode === 'hetzner') {
      try {
        hetznerProject = await api.getHetznerProject(profile.id);
        domain = hetznerProject.naming?.domain || domain;
        environmentExtension = hetznerProject.naming?.envExt || environmentExtension;
        hetznerToolchain = hetznerProject.toolchain ?? null;
        hetznerWorkspace = hetznerProject.workspace ?? null;
        hetznerTemporaryAccess = hetznerProject.temporaryAccess ?? null;
      } catch {
        hetznerProject = null;
      }
    }
    try {
      handoffAssessment = await api.getHandoffAssessment(profile.id);
    } catch {
      handoffAssessment = null;
    }
    // Every recorded action, not only the ones this browser happened to watch:
    // the stream replays from the beginning, which is what makes one activity
    // list for the whole installation possible at all.
    startEventStream(profile.id);
    runPurpose = (window.localStorage.getItem(`smallworlds.runPurpose.${profile.id}`) as typeof runPurpose) || '';
    const runID = window.localStorage.getItem(`smallworlds.run.${profile.id}`);
    if (runID) {
      try {
        run = await api.getRun(runID);
        if (run.state === 'running') schedulePoll(run.id);
      } catch {
        run = null;
        runPurpose = '';
        window.localStorage.removeItem(`smallworlds.run.${profile.id}`);
      }
    } else {
      run = null;
    }
    // Derived state settles after the awaits above, so pick the landing stage
    // last — otherwise a returning operator is dropped at step one regardless
    // of how far they had already got.
    activeTab = 'setup';
    openFirstOpenStep();
  }

  // --- Saved answers -------------------------------------------------------
  // Everything the operator types that is not a secret lives on the Launcher
  // Host, so closing the browser or restarting the launcher never costs them a
  // retyped domain, host name, or release tag. Secrets are deliberately absent
  // here: they travel only through the endpoints that write them to the vault.

  function collectSettings(): SetupSettings {
    return {
      capabilityMode,
      capabilityApps,
      release,
      settingsRepositoryUrl: capabilityRepositoryURL,
      domain,
      settingsProvider,
      githubAuthority: gitHubAuthority,
      githubRepositoryName: gitHubRepositoryName,
      gitUsername: genericGitUsername,
      nodeTargetKind,
      nodeHost,
      nodePort,
      nodeUsername,
      nodeAuthentication,
      dataDirectory: localBootstrapDataDirectory,
      nodeName: localBootstrapNodeName,
      environment: environmentExtension,
      acmeEmail: adminEmail,
      manageDns: localBootstrapManageDNS,
      routerAcknowledged: localPublicRouterAcknowledged,
      hetznerDomain: domain,
      hetznerEnvExt: environmentExtension,
      hetznerTier,
      hetznerLocation,
      hetznerServerType,
      hetznerVolumeGb,
      hetznerOperatorAddress,
      handoffBaseDomain,
      offsiteEndpoint,
      offsiteRegion,
      offsiteBucket
    };
  }

  function applySettings(saved: SetupSettings): void {
    hydrating = true;
    try {
      // Only overwrite a field when the saved answer is non-empty, so a profile
      // that predates a field keeps the component's sensible default.
      if (saved.capabilityMode) capabilityMode = saved.capabilityMode as CapabilityMode;
      if (saved.capabilityApps) capabilityApps = saved.capabilityApps;
      if (saved.release) release = saved.release;
      if (saved.settingsRepositoryUrl) capabilityRepositoryURL = saved.settingsRepositoryUrl;
      // Profiles written before the three domain fields were merged may carry
      // the answer under either name; both meant the same thing.
      if (saved.domain || saved.hetznerDomain) domain = saved.domain || saved.hetznerDomain || '';
      if (saved.environment || saved.hetznerEnvExt) environmentExtension = saved.environment || saved.hetznerEnvExt || '';
      if (saved.acmeEmail) adminEmail = saved.acmeEmail;
      if (saved.settingsProvider) settingsProvider = saved.settingsProvider as 'github' | 'generic';
      if (saved.githubAuthority) gitHubAuthority = saved.githubAuthority as 'creation' | 'ongoing';
      if (saved.githubRepositoryName) gitHubRepositoryName = saved.githubRepositoryName;
      if (saved.gitUsername) genericGitUsername = saved.gitUsername;
      if (saved.nodeTargetKind) nodeTargetKind = saved.nodeTargetKind as 'remote' | 'same-host';
      if (saved.nodeHost) nodeHost = saved.nodeHost;
      if (saved.nodePort) nodePort = saved.nodePort;
      if (saved.nodeUsername) nodeUsername = saved.nodeUsername;
      if (saved.nodeAuthentication) nodeAuthentication = saved.nodeAuthentication as typeof nodeAuthentication;
      if (saved.dataDirectory) localBootstrapDataDirectory = saved.dataDirectory;
      if (saved.nodeName) localBootstrapNodeName = saved.nodeName;
      localBootstrapManageDNS = saved.manageDns ?? false;
      localPublicRouterAcknowledged = saved.routerAcknowledged ?? false;
      if (saved.hetznerTier) hetznerTier = saved.hetznerTier as HetznerPresetTier;
      if (saved.hetznerLocation) hetznerLocation = saved.hetznerLocation;
      if (saved.hetznerServerType) hetznerServerType = saved.hetznerServerType;
      if (saved.hetznerVolumeGb) hetznerVolumeGb = saved.hetznerVolumeGb;
      if (saved.hetznerOperatorAddress) hetznerOperatorAddress = saved.hetznerOperatorAddress;
      if (saved.handoffBaseDomain) handoffBaseDomain = saved.handoffBaseDomain;
      if (saved.offsiteEndpoint) offsiteEndpoint = saved.offsiteEndpoint;
      if (saved.offsiteRegion) offsiteRegion = saved.offsiteRegion;
      if (saved.offsiteBucket) offsiteBucket = saved.offsiteBucket;
    } finally {
      hydrating = false;
    }
  }

  // Called from field handlers rather than an $effect: an effect over this many
  // sources would fire on every keystroke of every field and is far harder to
  // reason about than an explicit "the operator finished with this field" call.
  function rememberAnswers(): void {
    if (hydrating || !activeProfile) return;
    const profileId = activeProfile.id;
    if (settingsSaveTimer) window.clearTimeout(settingsSaveTimer);
    settingsSaveTimer = window.setTimeout(() => {
      void api.saveSettings(profileId, collectSettings()).catch(() => {
        // Saved answers are a convenience, never a precondition. A failed write
        // must not interrupt the operator mid-journey; the next edit retries.
      });
    }, 400);
  }

  /** Whether a secret of this kind is already in the safe for this profile.
   *  A stored secret is never sent back to the browser, so the only honest
   *  thing to show is that one exists — and to stop demanding it again. */
  function secretStored(kind: string): boolean {
    return credentials.some((credential) => credential.kind === kind && credential.present);
  }

  const gitHubTokenStored = $derived(secretStored(`github-${gitHubAuthority}-token`));
  const genericGitTokenStored = $derived(secretStored('generic-git-token'));
  const hetznerTokenStored = $derived(secretStored('hetzner-project-token'));
  const offsiteKeysStored = $derived(secretStored('offsite-s3-access-key') && secretStored('offsite-s3-secret'));
  const dnsTokenStored = $derived(secretStored('local-public-dns-token'));
  const nodePasswordStored = $derived(secretStored('node-password'));
  const nodePrivateKeyStored = $derived(secretStored('node-private-key'));

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

  /** What is still missing before an overlay can be established, or '' when
   *  nothing is. The establishment form used to vanish silently while any of
   *  this was outstanding, so an operator could not tell whether the console
   *  offered to create the repository at all. */
  const settingsRepoMissing: MessageKey | '' = $derived.by(() => {
    if (!capabilityPlan) return 'settingsRepoNeedsPlan';
    if (settingsProvider === 'github') {
      if (!gitHubStatus) return 'settingsRepoNeedsToken';
      if (gitHubStatus.authority !== 'creation') return 'settingsRepoNeedsCreationAuthority';
      return '';
    }
    return genericGitStatus ? '' : 'settingsRepoNeedsAccess';
  });

  /** Plain language for what the node section can refuse. It showed bare codes,
   *  which is how an operator ends up reading "node_host_key_confirmation_required"
   *  while looking at the confirmation button they just pressed. */
  function nodeErrorMessage(code: string): string {
    switch (code) {
      case 'node_host_key_confirmation_required': return message('nodeFingerprintExpired');
      case 'node_host_key_mismatch': return message('localBootstrapHostKeyMismatch');
      case 'node_connection_failed': return message('nodeConnectionFailed');
      case 'invalid_node_credentials': return message('nodeCredentialsRejected');
      case 'invalid_node_target': return message('nodeTargetInvalid');
      case 'node_sudo_authorization_failed': return message('nodeSudoRejected');
      case 'same_host_clean_failed':
      case 'node_clean_failed': return message('nodeCleanFailed');
      case 'vault_locked': return message('handoffUnlockFirst');
      default: return code;
    }
  }

  function settingsRepoErrorMessage(code: string): string {
    switch (code) {
      case 'github_repository_not_empty': return message('githubRepositoryNotEmpty');
      case 'generic_git_remote_conflict': return message('genericGitRemoteNotEmpty');
      case 'generic_git_overlay_identity_conflict': return message('genericGitIdentityConflict');
      case 'generic_git_authentication_failed': return message('genericGitAuthenticationFailed');
      case 'generic_git_credentials_missing': return message('settingsRepoNeedsAccess');
      case 'generic_git_resume_check_failed': return message('genericGitResumeCheckFailed');
      case 'github_repository_not_private': return message('githubRepositoryNotPrivate');
      case 'github_creation_token_missing': return message('settingsRepoNeedsCreationAuthority');
      case 'github_token_insufficient_authority': return message('settingsRepoTokenAuthority');
      case 'github_rate_limited': return message('settingsRepoRateLimited');
      case 'github_overlay_initialization_failed': return message('settingsRepoWriteFailed');
      case 'github_overlay_plan_not_approved':
      case 'plan_precondition_changed': return message('settingsRepoPlanStale');
      case 'vault_locked': return message('handoffUnlockFirst');
      default: return code;
    }
  }

  /** Plain language for the refusals an operator can act on. Anything else falls
   *  through to its code rather than being softened into something vague — an
   *  unexplained code is bad, but a wrong explanation is worse. */
  function localBootstrapErrorMessage(code: string): string {
    switch (code) {
      case 'gitops_overlay_required': return message('localBootstrapOverlayRequired');
      case 'local_bootstrap_release_mismatch': return message('localBootstrapReleaseMismatch')
        .replaceAll('{overlay}', overlayIdentity?.release ?? release);
      case 'local_bootstrap_release_unsupported': return message('localBootstrapReleaseUnsupported');
      case 'local_bootstrap_domain_mismatch': return message('localBootstrapDomainMismatch');
      case 'bootstrap_assets_not_ready': return message('localBootstrapAssetsNotReady');
      case 'bootstrap_asset_release_unavailable': return message('bootstrapAssetUnavailable');
      case 'node_host_key_confirmation_required': return message('localBootstrapHostKeyRequired');
      case 'node_host_key_mismatch': return message('localBootstrapHostKeyMismatch');
      case 'node_reinspection_failed': return message('localBootstrapReinspectionFailed');
      case 'invalid_cluster_secrets_manifest': return message('localBootstrapInvalidSecrets');
      case 'dns_provider_token_required': return message('localBootstrapDNSTokenRequired');
      case 'router_forwarding_acknowledgement_required': return message('localBootstrapRouterRequired');
      case 'vault_locked': return message('handoffUnlockFirst');
      default: return code;
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

  function handoffStepComplete(name: string): boolean {
    return handoffAssessment?.steps.some((step) => step.name === name && step.complete) === true;
  }

  async function refreshHandoffAssessment(): Promise<void> {
    if (!activeProfile) return;
    try {
      handoffAssessment = await api.getHandoffAssessment(activeProfile.id);
    } catch {
      // A profile with nothing established yet has no assessment to report.
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

  // --- Making administration private ---------------------------------------
  // Six things have to happen in one order, and every one of them was a button
  // of its own — ten buttons in five rows, with nothing to say which came
  // first. They are grouped here into the six decisions an operator actually
  // makes, and the stage offers only the next one.

  const establishTrust = () => runHandoff(async () => {
    await api.establishClusterCA(activeProfile!.id);
    deviceTrustFingerprint = (await api.installClusterCADeviceTrust(activeProfile!.id)).fingerprint;
  });
  const establishPrivateNetwork = () => runHandoff(() => api.establishPrivateNetwork(activeProfile!.id, handoffBaseDomain));
  const joinPrivateNetwork = () => runHandoff(async () => {
    if (!handoffStepComplete('gateway-identity')) await api.establishEnrollment(activeProfile!.id);
    await api.consumeLauncherEnrollment(activeProfile!.id);
  });
  const verifyHandoff = () => runHandoff(() => api.verifyHandoff(activeProfile!.id));
  const closeTemporaryAccess = () => runHandoff(() => api.closeTemporaryAccess(activeProfile!.id));
  const registerOwner = () => runHandoff(async () => {
    const challenge = firstOwnerChallenge || (await api.claimFirstOwner(activeProfile!.id)).claim.challenge;
    firstOwnerChallenge = challenge;
    const created = await navigator.credentials.create({
      publicKey: {
        challenge: base64urlToBytes(challenge),
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

  type HandoffAction = { stepName: string; labelKey: MessageKey; act: () => Promise<void> };

  /** The order the launcher will actually accept, which is not the order the
   *  assessment lists its evidence in: an enrolment cannot be consumed before
   *  it has been created, however the two are reported. */
  const handoffActions: HandoffAction[] = $derived.by(() => {
    const list: HandoffAction[] = [];
    if (activeProfile?.deploymentMode === 'local-lan') {
      list.push({ stepName: 'cluster-ca-trust-installed', labelKey: 'handoffActionCA', act: establishTrust });
    }
    list.push({ stepName: 'private-network', labelKey: 'handoffPrivateNetworkEstablish', act: establishPrivateNetwork });
    list.push({ stepName: 'launcher-enrolled', labelKey: 'handoffActionEnroll', act: joinPrivateNetwork });
    list.push({ stepName: 'handoff-verified', labelKey: 'handoffVerify', act: verifyHandoff });
    list.push({ stepName: 'temporary-access-closed', labelKey: 'handoffCloseAccess', act: closeTemporaryAccess });
    list.push({ stepName: 'first-owner-registered', labelKey: 'handoffActionOwner', act: registerOwner });
    return list;
  });

  const nextHandoffAction = $derived(handoffActions.find((action) => !handoffStepComplete(action.stepName)));

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

  /** What the repository will be called in the preview. For GitHub the real
   *  address only exists once the repository does, so the launcher previews
   *  against the same placeholder it will substitute at establishment. */
  const previewRepositoryURL = $derived.by(() =>
    settingsProvider === 'generic' && capabilityRepositoryURL
      ? capabilityRepositoryURL
      : `https://github.com/placeholder/${gitHubRepositoryName || 'smallworlds-overlay'}.git`
  );

  async function planCapabilities(): Promise<void> {
    if (!activeProfile) return;
    capabilityBusy = true;
    capabilityError = '';
    try {
      capabilityPlan = await api.planCapabilities({ environmentExtension, profileId: activeProfile.id, mode: capabilityMode, communityIds: capabilityApps, release, repositoryUrl: previewRepositoryURL, domain });
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
      await ensureCapabilityPlan();
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
      // The launcher only establishes against an approved plan, and the file
      // listing the operator just read is what this click approves. Doing it
      // here keeps the approval part of the action instead of a separate step
      // nothing points to.
      await api.approvePlan(capabilityPlan.plan.id);
      const identity = await api.establishGitHubOverlay({ environmentExtension, profileId: activeProfile.id, planId: capabilityPlan.plan.id, repositoryName: gitHubRepositoryName, mode: capabilityMode, communityIds: capabilityApps, release, domain });
      // Treat a just-established overlay exactly like a reopened one, so the
      // stage shows what was recorded and the install picks up its address.
      overlayIdentity = identity;
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
      await ensureCapabilityPlan();
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
      await api.approvePlan(capabilityPlan.plan.id);
      const identity = await api.establishGenericGitOverlay({ environmentExtension, profileId: activeProfile.id, planId: capabilityPlan.plan.id, repositoryUrl: capabilityRepositoryURL, mode: capabilityMode, communityIds: capabilityApps, release, domain });
      overlayIdentity = identity;
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
      await api.approvePlan(capabilityPlan.plan.id);
      genericGitProposal = await api.proposeGenericGitOverlay({ environmentExtension, profileId: activeProfile.id, planId: capabilityPlan.plan.id, repositoryUrl: capabilityRepositoryURL, mode: capabilityMode, communityIds: capabilityApps, release, domain });
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
      hetznerProject = await api.inspectHetznerProject(activeProfile.id, domain, environmentExtension);
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
        acmeEmail: adminEmail
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

  /** Removes one installation from this computer. Nothing outside this machine
   *  is touched — an installation that was never provisioned simply disappears,
   *  and one that was keeps running until it is retired deliberately. */
  async function forgetProfile(target: ClusterProfile): Promise<void> {
    if (!window.confirm(decommissionMessage('forgetConfirm').replace('{name}', target.name))) return;
    decommissionBusy = true;
    decommissionError = '';
    try {
      await api.forgetProfile(target.id);
      profiles = profiles.filter((profile) => profile.id !== target.id);
      if (activeProfile?.id !== target.id) return;
      activeProfile = profiles[0] ?? null;
      decommissionPlan = null;
      decommissionRun = null;
      showRecovery = false;
      editing = false;
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

  function currentNodeTarget(): NodeTarget {
    return nodeTargetKind === 'same-host' ? { kind: 'same-host' } : { kind: 'remote', host: nodeHost, port: nodePort, username: nodeUsername };
  }

  function currentNodeAuthentication() {
    return { kind: nodeAuthentication, ...(nodePassword ? { password: nodePassword } : {}), ...(nodePrivateKey ? { privateKey: nodePrivateKey } : {}), ...(nodeKeyPassphrase ? { keyPassphrase: nodeKeyPassphrase } : {}), ...(nodeSudoPassword ? { sudoPassword: nodeSudoPassword } : {}) };
  }

  function clearNodeSecrets(): void {
    nodePassword = '';
    nodePrivateKey = '';
    nodeKeyPassphrase = '';
    nodeSudoPassword = '';
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
      nodeTrust = await api.getNodeTrust(activeProfile.id);
    } catch (reason) {
      nodeError = reason instanceof Error ? reason.message : 'node_host_key_confirmation_required';
      // The launcher only accepts a confirmation for a fingerprint it observed in
      // the last ten minutes. Keeping the stale reading on screen left the button
      // failing forever, telling the operator to confirm what they were
      // confirming; dropping it puts them back at the reading itself.
      if (nodeError === 'node_host_key_confirmation_required') nodeProbe = null;
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
      nodeInspection = await api.inspectNode(activeProfile.id, currentNodeTarget(), currentNodeAuthentication(), localBootstrapDataDirectory);
      clearNodeSecrets();
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
      sshKeyPlan = await api.planNodeSSHKey(activeProfile.id);
    } catch (reason) {
      nodeError = reason instanceof Error ? reason.message : 'node_ssh_key_plan_failed';
    } finally {
      nodeBusy = false;
    }
  }

  /** Deletes what the inspection found in the way. Irreversible and unbacked,
   *  so the exact paths are named once more at the point of no return. */
  async function cleanNode(): Promise<void> {
    if (!activeProfile) return;
    const target = nodeTargetKind === 'remote' ? nodeHost : message('nodeSameHost');
    const confirmation = message('foreignInstallConfirm')
      .replace('{host}', target)
      .replace('{paths}', foreignRemovalPaths.join('\n'));
    if (!window.confirm(confirmation)) return;
    cleanNodeBusy = true;
    nodeError = '';
    try {
      await api.cleanNode(activeProfile.id, currentNodeTarget(), currentNodeAuthentication(), localBootstrapDataDirectory);
      await inspectNode();
    } catch (reason) {
      nodeError = reason instanceof Error ? reason.message : 'node_clean_failed';
    } finally {
      cleanNodeBusy = false;
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
        authentication: currentNodeAuthentication(),
        // The release this project was pinned to when its settings repository was
        // established. The launcher installs any release whose signed manifest
        // verifies, so there is nothing to hardcode here.
        release: overlayIdentity?.release || release,
        configuration: { domain: overlayIdentity?.domain || domain, environmentExtension, dataDirectory: localBootstrapDataDirectory, nodeName: localBootstrapNodeName, acmeEmail: adminEmail, manageDns: localBootstrapManageDNS },
        ...(activeProfile.deploymentMode === 'local-public' ? { publicExposure: { dns01Provider: 'hetzner' as const, dnsZone: domain, dnsToken: localPublicDNSToken, publicIpBehavior: 'dynamic-ddns' as const, routerAcknowledged: localPublicRouterAcknowledged } } : {}),
        ...(localBootstrapSecrets ? { secretsManifest: localBootstrapSecrets } : {})
      });
      installPlan = result.plan;
      nodeInspection = result.inspection;
      localBootstrapSecrets = '';
      localPublicDNSToken = '';
      clearNodeSecrets();
    } catch (reason) {
      localBootstrapError = reason instanceof Error ? reason.message : 'local_bootstrap_plan_failed';
    } finally {
      localBootstrapBusy = false;
    }
  }

  /** Says in words what an inspection found in the way. The launcher reports
   *  these as codes; showing the code alone left the operator to guess whether
   *  a machine was unsuitable, occupied, or merely already half set up. */
  function blockerLabel(code: string): string {
    const port = /^port\.(\d+)\.occupied$/.exec(code);
    if (port) return message('blockerPortOccupied').replace('{port}', port[1]);
    const labels: Record<string, MessageKey> = {
      'node.os.unsupported': 'blockerOsUnsupported',
      'node.systemd.missing': 'blockerSystemdMissing',
      'node.kernel.unsupported': 'blockerKernelUnsupported',
      'node.privilege.unavailable': 'blockerPrivilegeUnavailable',
      'capacity.memory.insufficient': 'blockerMemoryInsufficient',
      'capacity.disk.insufficient': 'blockerDiskInsufficient',
      'installation.profile.mismatch': 'blockerProfileMismatch',
      'installation.kubernetes.foreign': 'blockerKubernetesForeign',
      'installation.kubernetes.unknown': 'blockerKubernetesUnknown',
      'installation.kubernetes.existing': 'blockerKubernetesExisting',
      'installation.data.foreign': 'blockerDataForeign',
      'installation.data.unknown': 'blockerDataUnknown',
      'installation.data.existing': 'blockerDataExisting'
    };
    return labels[code] ? message(labels[code]) : code;
  }

  /** Exactly what "remove what is in the way" deletes, in the order the
   *  launcher deletes it. Mirrors the command in clean_node.go — the operator
   *  is asked to approve this list, so it may not be an approximation. */
  const foreignRemovalPaths = $derived([
    '/var/lib/rancher/k3s',
    '/etc/rancher',
    '/etc/smallworlds',
    localBootstrapDataDirectory
  ]);

  /** Names one entry in the safe. Every row used to claim it was the settings
   *  repository token, whatever it actually held; an unknown kind now shows its
   *  own identifier rather than borrowing someone else's name. */
  function credentialLabel(kind: string): string {
    const labels: Record<string, MessageKey> = {
      'git-provider-token': 'gitProviderToken',
      'github-creation-token': 'githubToken',
      'github-ongoing-token': 'githubToken',
      'generic-git-token': 'genericGitToken',
      'hetzner-project-token': 'hetznerToken',
      'local-public-dns-token': 'localPublicDNSToken',
      'node-password': 'nodePassword',
      'node-private-key': 'nodePrivateKey',
      'node-sudo-password': 'nodeSudoPassword',
      'offsite-s3-access-key': 'offsiteAccessKey',
      'offsite-s3-secret': 'offsiteSecretKey'
    };
    return labels[kind] ? message(labels[kind]) : kind;
  }

  function rotationLabel(status: string): string {
    if (status === 'expired') return message('rotationExpired');
    if (status === 'due-soon') return message('rotationDueSoon');
    return message('rotationCurrent');
  }

  function showCreateProfile(): void {
    creating = true;
    editing = false;
    showRecovery = false;
    profileName = '';
    profileLanguage = locale;
    deploymentMode = 'local-lan';
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

  async function createSelfTestPlan(): Promise<void> {
    if (!activeProfile) return;
    selfTestBusy = true;
    error = '';
    try {
      selfTestPlan = await api.createVerificationPlan(activeProfile.id);
    } catch (reason) {
      error = reason instanceof Error ? reason.message : 'request_failed';
    } finally {
      selfTestBusy = false;
    }
  }

  /** Approving is the only step that starts work, so which plan is being
   *  approved decides what a verified run afterwards proves. */
  async function approve(plan: ChangePlan | null, purpose: 'install' | 'self-test'): Promise<void> {
    if (!plan || !activeProfile) return;
    busy = true;
    error = '';
    try {
      run = await api.approvePlan(plan.id);
      runPurpose = purpose;
      window.localStorage.setItem(`smallworlds.run.${activeProfile.id}`, run.id);
      window.localStorage.setItem(`smallworlds.runPurpose.${activeProfile.id}`, purpose);
      schedulePoll(run.id);
    } catch (reason) {
      error = reason instanceof Error ? reason.message : 'request_failed';
    } finally {
      busy = false;
    }
  }

  async function approveHetznerPlan(): Promise<void> {
    if (!hetznerPlan?.plan) return;
    hetznerError = '';
    await approve(hetznerPlan.plan, 'install');
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
        else if (runPurpose === 'install' && run.state === 'verified') await refreshHandoffAssessment();
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

  /** One line of the record, in words. The raw type is kept beside it: it is
   *  what a support request will be asked to quote. */
  function activityLabel(type: string): string {
    const labels: Record<string, MessageKey> = {
      'run.started': 'activityRunStarted',
      'run.checkpoint': 'activityRunCheckpoint',
      'run.failed': 'activityRunFailed',
      'offsite.proposal.opened': 'activityOffsiteProposed',
      'full-decommission.approved': 'activityDecommissionApproved',
      'full-decommission.completed': 'activityDecommissionCompleted'
    };
    return labels[type] ? message(labels[type]) : type;
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
          <!-- Removing an installation lives next to the installation it removes.
               It only makes this computer forget it; the cluster, if there is one,
               keeps running — which is what the confirmation says. -->
          <div class="profile-row" class:active={activeProfile?.id === profile.id}>
            <button class="profile-pick" class:active={activeProfile?.id === profile.id} onclick={() => { creating = false; editing = false; showRecovery = false; void selectProfile(profile); }}>
              <span>{profile.name}</span>
              <small>{profile.deploymentMode}</small>
            </button>
            <button
              class="profile-remove"
              title={decommissionMessage('forget')}
              aria-label={`${message('removeProfile')} — ${profile.name}`}
              onclick={() => void forgetProfile(profile)}
              disabled={decommissionBusy}
            >✕</button>
          </div>
        {/each}
      </nav>
      <button class="secondary full" onclick={showCreateProfile}>{message('createAnother')}</button>

      {#if activeProfile && !creating}
        <!-- The state of the installation as a whole, not of the open step. It
             says plainly that nothing is running rather than claiming a
             readiness the launcher has not established. -->
        <h2 class="aside-heading">{message('statusHeading')}</h2>
        <div class="aside-status" class:verified={run?.state === 'verified'} role="status" aria-live="polite" aria-atomic="true">
          <span class="status-dot" aria-hidden="true"></span>
          <div>
            <span>{run ? runLabel(run.state) : message('statusIdle')}</span>
            {#if run}<small>{run.currentCheckpoint || message('running')}</small>{/if}
          </div>
        </div>
        {#if run?.state === 'running' && run.cancellationState === 'not-requested'}
          <button class="secondary full" onclick={() => void cancelRun()} disabled={busy}>{message('cancel')}</button>
        {/if}
      {/if}
    </aside>

    <main id="main-content" tabindex="-1">
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
        </section>

        <!-- The safe opens with this computer's own login wherever that works,
             which is what an operator would have chosen anyway. It only asks
             when it genuinely cannot — so this is a prompt, not a permanent
             panel someone has to read past on every screen. -->
        {#if vaultStatus?.state !== 'unlocked'}
          <section class="card vault-card" aria-labelledby="vault-title">
            <div class="vault-heading">
              <div>
                <p class="eyebrow">{message('vaultEyebrow')}</p>
                <h2 id="vault-title">{message('vaultTitle')}</h2>
              </div>
              <span class="badge">{message('vaultLocked')}</span>
            </div>
            <p class="muted">{message('vaultDescription')}</p>
            {#if vaultError}<p class="inline-error" role="alert">{vaultError}</p>{/if}
            {#if vaultStatus?.osCredentialStoreAvailable}
              <div class="actions"><button onclick={() => void unlockVault('operating-system')} disabled={vaultBusy}>{message('unlockWithOSStore')}</button></div>
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
          </section>
        {/if}

        <!-- Three places, not one long page: the work of setting an installation
             up, the record of what has been done, and the installation itself. -->
        <nav class="tabs" aria-label={message('sections')}>
          <div role="tablist" aria-label={message('sections')}>
            <button role="tab" id="tab-setup" aria-controls="panel-setup" aria-selected={activeTab === 'setup'} tabindex={activeTab === 'setup' ? 0 : -1} class:selected={activeTab === 'setup'} onclick={() => activeTab = 'setup'}>{message('tabSetup')}</button>
            <button role="tab" id="tab-activity" aria-controls="panel-activity" aria-selected={activeTab === 'activity'} tabindex={activeTab === 'activity' ? 0 : -1} class:selected={activeTab === 'activity'} onclick={() => activeTab = 'activity'}>{message('tabActivity')}</button>
            <button role="tab" id="tab-manage" aria-controls="panel-manage" aria-selected={activeTab === 'manage'} tabindex={activeTab === 'manage' ? 0 : -1} class:selected={activeTab === 'manage'} onclick={() => { activeTab = 'manage'; showRecovery = false; }}>{message('tabManage')}</button>
          </div>
        </nav>

        {#if activeTab === 'setup'}
        <div id="panel-setup" role="tabpanel" aria-labelledby="tab-setup" tabindex="0">
          <!-- One thing to do, named. Everything else on this screen is context
               for it. -->
          <section class="next-action">
            <p class="eyebrow">{message('next')}</p>
            {#if nextStep}
              <h2 id="next-title">{message(nextStep.titleKey)}</h2>
              <p class="muted">{message(nextStep.summaryKey)}</p>
              {#if nextStep.id !== activeStep}
                <div class="actions"><button type="button" onclick={() => goToStep(nextStep.id)}>{message('nextActionGo')}</button></div>
              {/if}
            {:else}
              <h2 id="next-title">{message('nextActionDone')}</h2>
            {/if}
          </section>

          <!-- The whole journey at a glance. A stage that cannot be worked on yet
               stays listed and says why, rather than vanishing and leaving the
               operator unsure whether it exists. -->
          <nav class="journey-rail" aria-label={message('journeyProgress')}>
            <ol>
              {#each steps as step, index (step.id)}
                <li class:done={step.done} class:current={step.id === activeStep} class:locked={!!step.blockedKey}>
                  <!-- Deliberately not disabled. An operator may look ahead to see
                       what is coming; the step itself still says what is missing,
                       and the launcher refuses the action regardless. Locking the
                       navigation would only hide the shape of the work. -->
                  <button
                    type="button"
                    onclick={() => goToStep(step.id)}
                    aria-current={step.id === activeStep ? 'step' : undefined}
                  >
                    <span class="rail-index" aria-hidden="true">{step.done ? '✓' : index + 1}</span>
                    <span class="rail-title">{message(step.titleKey)}</span>
                    <span class="rail-state">
                      {#if step.done}{message('stepDone')}
                      {:else if step.id === activeStep}{message('stepCurrent')}
                      {:else if step.blockedKey}{message(step.blockedKey)}
                      {/if}
                    </span>
                  </button>
                </li>
              {/each}
            </ol>
          </nav>

          {#if currentStep}
            <section class="step-heading">
              <p class="eyebrow">{message('journeyProgress')}</p>
              <h2 id="step-heading-title">{message(currentStep.titleKey)}</h2>
              <p class="muted">{message(currentStep.summaryKey)}</p>
              {#if currentStep.blockedKey}
                <p class="inline-notice">{message(currentStep.blockedKey)}</p>
              {/if}
            </section>
          {/if}

          {#if activeStep === 'design'}
            <!-- Everything the community gets, and what it will be called. These
                 answers are asked for here once and read from here everywhere
                 else; nothing later asks for the domain a second time. -->
            <section class="card capability-card" aria-labelledby="capability-title">
              <p class="eyebrow">{message('capabilityEyebrow')}</p>
              <h2 id="capability-title">{message('capabilityTitle')}</h2>
              <p class="muted">{message('capabilityDescription')}</p>
              {#if capabilityError}<p class="inline-error" role="alert">{capabilityError}</p>{/if}
              <form onsubmit={(event) => { event.preventDefault(); void planCapabilities(); }} onchange={rememberAnswers}>
                <label><span>{message('capabilityMode')}</span><select bind:value={capabilityMode} onchange={() => capabilityPlan = null}><option value="minimal">{message('capabilityMinimal')}</option><option value="collaboration">{message('capabilityCollaboration')}</option><option value="full">{message('capabilityFull')}</option><option value="custom">{message('capabilityCustom')}</option></select></label>
                {#if capabilityMode === 'custom'}
                  <fieldset><legend>{message('capabilityCommunityApps')}</legend>{#each capabilityCatalog?.capabilities.filter((entry) => entry.category === 'community-application') ?? [] as entry (entry.id)}<label class="check"><input type="checkbox" checked={capabilityApps.includes(entry.id)} onchange={(event) => toggleCapability(entry.id, (event.currentTarget as HTMLInputElement).checked)} /><span>{entry.id} · {entry.resources.memoryMi} MiB / {entry.resources.storageGi} GiB</span></label>{/each}</fieldset>
                {/if}
                <p class="muted">{message('designPlatformHint')}</p>

                <h3>{message('identityHeading')}</h3>
                <div class="form-grid">
                  <label><span>{message('capabilityDomain')}</span><input bind:value={domain} required placeholder="home.example" /></label>
                  <label><span>{message('identityEmail')}</span><input type="email" bind:value={adminEmail} placeholder="operator@example.org" /><small class="muted">{message('identityEmailHint')}</small></label>
                </div>

                <div class="actions"><button type="submit" disabled={capabilityBusy}>{message('capabilityReview')}</button></div>
              </form>

              <!-- Values that are right for almost everyone are already filled
                   in. They stay reachable, out of the way of the decision. -->
              <div class="advanced">
                <button type="button" class="secondary" aria-expanded={advancedDesign} onclick={() => advancedDesign = !advancedDesign}>{advancedDesign ? message('advancedHide') : message('advancedShow')}</button>
                {#if advancedDesign}
                  <div class="advanced-body">
                    <p class="muted">{message('advancedHint')}</p>
                    <div class="form-grid">
                      <label><span>{message('capabilityRelease')}</span><input bind:value={release} required pattern="v[0-9]+\.[0-9]+\.[0-9]+.*" onchange={rememberAnswers} /></label>
                      <label><span>{message('localBootstrapEnvironment')}</span><input bind:value={environmentExtension} pattern={environmentExtensionPattern} placeholder=".dev" onchange={rememberAnswers} /><small class="muted">{message('capabilityEnvExtHint')}</small></label>
                    </div>
                  </div>
                {/if}
              </div>

              {#if capabilityPlan}
                <section class="capability-preview">
                  <p class="eyebrow">{message('capabilityPreview')}</p>
                  <h3 id="capability-preview-title">{message('designSummaryHeading')}</h3>
                  <dl>
                    <!-- A selection with no community applications is a valid
                         installation, and its list arrives absent rather than
                         empty — so every one of these is read defensively. -->
                    <div><dt>{message('designApps')}</dt><dd>{(capabilityPlan.overlay.assessment.communityIds ?? []).join(', ') || message('designAppsNone')}</dd></div>
                    <div><dt>{message('capabilityMemory')}</dt><dd>{capabilityPlan.overlay.assessment.resources.memoryMi} MiB</dd></div>
                    <div><dt>{message('capabilityStorage')}</dt><dd>{capabilityPlan.overlay.assessment.resources.storageGi} GiB</dd></div>
                    <div><dt>{message('designExposure')}</dt><dd>{(capabilityPlan.overlay.assessment.exposure ?? []).join(', ') || '—'}</dd></div>
                    <div><dt>{message('designProtection')}</dt><dd>{(capabilityPlan.overlay.assessment.protection ?? []).join(', ') || '—'}</dd></div>
                  </dl>
                </section>
              {/if}
            </section>
          {/if}

          {#if activeStep === 'settings-repo'}
            <!-- Before any machine is touched: the cluster is installed by being
                 pointed at this repository, so it has to exist and already hold
                 the configuration by then. -->
            <section class="card github-card" aria-labelledby="settings-repo-title">
              <p class="eyebrow">{message('stepSettingsRepoTitle')}</p>
              <h2 id="settings-repo-title">{message('stepSettingsRepoTitle')}</h2>
              <p class="muted">{message('stepSettingsRepoSummary')}</p>

              <!-- The repository an earlier session established. Without this an
                   existing cluster looks as though it had none, and the obvious
                   next move would be to establish a second one. -->
              {#if overlayIdentity}
                <dl class="credential-metadata" data-testid="recorded-overlay">
                  <div><dt>{message('overlayRecordedRepository')}</dt><dd><a href={overlayIdentity.repositoryUrl} target="_blank" rel="noreferrer">{overlayIdentity.repository}</a></dd></div>
                  <div><dt>{message('capabilityRelease')}</dt><dd><code>{overlayIdentity.release}</code></dd></div>
                  <div><dt>{message('overlayRecordedCommit')}</dt><dd><code>{overlayIdentity.commit.slice(0, 12)}</code></dd></div>
                  <div><dt>{message('overlayRecordedAt')}</dt><dd>{formatDateTime(locale, overlayIdentity.recordedAt)}</dd></div>
                </dl>
                <p class="muted">{message('overlayRecordedHint')}</p>
              {:else}
                <!-- One decision up front, instead of two skippable provider cards
                     that gave no hint they were alternatives rather than both
                     required. -->
                <fieldset class="provider-choice">
                  <legend>{message('settingsRepoChoice')}</legend>
                  <label class="check"><input type="radio" name="settings-provider" value="github" checked={settingsProvider === 'github'} onchange={() => { settingsProvider = 'github'; rememberAnswers(); void ensureCapabilityPlan(); }} /><span>{message('settingsRepoGitHub')}</span></label>
                  <label class="check"><input type="radio" name="settings-provider" value="generic" checked={settingsProvider === 'generic'} onchange={() => { settingsProvider = 'generic'; rememberAnswers(); }} /><span>{message('settingsRepoGeneric')}</span></label>
                </fieldset>

                {#if settingsProvider === 'github'}
                  <p class="muted">{message('githubDescription')} <a href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer">{message('githubTokenGuide')}</a></p>
                  {#if gitHubError}<p class="inline-error" role="alert">{settingsRepoErrorMessage(gitHubError)}</p>{/if}
                  <form onsubmit={(event) => { event.preventDefault(); void validateGitHubToken(); }} onchange={rememberAnswers}>
                    <!-- The name appears in the file listing below, so changing
                         it rebuilds that listing rather than leaving a preview
                         that names a repository nobody asked for. -->
                    <label><span>{message('githubRepositoryName')}</span><input bind:value={gitHubRepositoryName} required pattern="[A-Za-z0-9._-]+" onchange={() => void ensureCapabilityPlan()} /><small class="muted">{message('githubRepositoryHint')}</small></label>
                    <label><span>{message('githubToken')}</span><input type="password" bind:value={gitHubToken} required={!gitHubTokenStored} autocomplete="off" />{#if gitHubTokenStored}<small class="muted">{message('secretAlreadySaved')}</small>{/if}</label>
                    <div class="actions"><button type="submit" disabled={gitHubBusy}>{message('githubValidate')}</button></div>
                  </form>
                  {#if gitHubStatus}<dl class="credential-metadata"><div><dt>{message('githubOwner')}</dt><dd>{gitHubStatus.owner}</dd></div><div><dt>{message('credentialExpires')}</dt><dd>{gitHubStatus.expiresAt || message('githubNoExpiry')}</dd></div><div><dt>{message('githubAuthority')}</dt><dd>{gitHubStatus.authority === 'creation' ? message('githubCreationAuthority') : message('githubOngoingAuthority')}</dd></div></dl>{/if}
                {:else if settingsProvider === 'generic'}
                  <p class="muted">{message('genericGitDescription')}</p>
                  {#if genericGitError}<p class="inline-error" role="alert">{settingsRepoErrorMessage(genericGitError)}</p>{/if}
                  <form onsubmit={(event) => { event.preventDefault(); void validateGenericGitCredentials(); }} onchange={rememberAnswers}>
                    <label><span>{message('capabilityRepository')}</span><input type="url" bind:value={capabilityRepositoryURL} required placeholder="https://git.example.org/you/private-overlay.git" /></label>
                    <div class="form-grid"><label><span>{message('genericGitUsername')}</span><input bind:value={genericGitUsername} required={!genericGitTokenStored} autocomplete="username" /></label><label><span>{message('genericGitToken')}</span><input type="password" bind:value={genericGitToken} required={!genericGitTokenStored} autocomplete="off" />{#if genericGitTokenStored}<small class="muted">{message('secretAlreadySaved')}</small>{/if}</label></div>
                    <div class="actions"><button type="submit" disabled={genericGitBusy}>{message('genericGitValidate')}</button></div>
                  </form>
                  {#if genericGitStatus}<p class="inline-notice">{genericGitStatus.repositoryUrl}</p>{/if}
                {/if}

                <!-- The whole configuration, in readable form, immediately before
                     the click that writes it. This is the moment the spec calls
                     an exact file listing, and it belongs here rather than three
                     stages earlier where it could not yet be accurate. -->
                {#if settingsProvider !== ''}
                  <section class="capability-preview">
                    <p class="eyebrow">{message('capabilityPreview')}</p>
                    <h3 id="settings-repo-files-title">{message('settingsRepoFilesHeading')}</h3>
                    <p class="muted">{message('settingsRepoFilesHint')}</p>
                    {#if capabilityBusy}
                      <p class="muted">{message('settingsRepoPreparing')}</p>
                    {:else if capabilityPlan}
                      <div data-testid="overlay-diff" class="overlay-diff" role="textbox" aria-readonly="true" tabindex="0" aria-label={message('capabilityOverlayDiff')}>{capabilityPlan.overlay.diff}</div>
                    {/if}
                    {#if settingsRepoMissing !== ''}
                      <p class="inline-notice">{message(settingsRepoMissing)}</p>
                    {:else if settingsProvider === 'github'}
                      <div class="actions"><button type="button" onclick={() => void establishGitHubOverlay()} disabled={gitHubBusy}>{message('githubEstablish')}</button></div>
                    {:else}
                      <p class="muted">{message('genericGitApprovalHint')}</p>
                      <div class="actions">
                        <button type="button" class="secondary" onclick={() => void proposeGenericGitOverlay()} disabled={genericGitBusy}>{message('genericGitPropose')}</button>
                        <button type="button" onclick={() => void establishGenericGitOverlay()} disabled={genericGitBusy}>{message('genericGitEstablish')}</button>
                      </div>
                    {/if}
                  </section>
                {/if}
                {#if gitHubOverlayNotice}<p class="inline-notice" aria-live="polite">{gitHubOverlayNotice}</p>{/if}
                {#if genericGitOverlayNotice}<p class="inline-notice" aria-live="polite">{genericGitOverlayNotice}</p>{/if}
                {#if genericGitProposal}<p class="inline-notice" aria-live="polite">{message('genericGitManualMerge')} <code>{genericGitProposal.branch}</code> · {genericGitProposal.commit}</p>{/if}
              {/if}
            </section>
          {/if}

          {#if activeStep === 'machine' && !rentsMachine}
            <section class="card node-card" aria-labelledby="node-title">
              <p class="eyebrow">{message('nodeEyebrow')}</p>
              <h2 id="node-title">{message('nodeTitle')}</h2>
              <p class="muted">{message('nodeDescription')}</p>
              {#if nodeError}<p class="inline-error" role="alert">{nodeErrorMessage(nodeError)}</p>{/if}
              <!-- What an earlier session already confirmed about this machine. Shown
                   before the form, so a returning operator recognises the cluster
                   instead of facing fields that look like a fresh start. -->
              {#if nodeTrust}
                <dl class="credential-metadata" data-testid="recorded-node">
                  <div><dt>{message('nodeRecordedHost')}</dt><dd><code>{nodeTrust.host}:{nodeTrust.port}</code></dd></div>
                  <div><dt>{message('nodeUsername')}</dt><dd><code>{nodeTrust.username}</code></dd></div>
                  <div><dt>{message('nodeFingerprint')}</dt><dd><code>{nodeTrust.fingerprint}</code></dd></div>
                  <div><dt>{message('nodeRecordedConfirmedAt')}</dt><dd>{formatDateTime(locale, nodeTrust.confirmedAt)}</dd></div>
                </dl>
                <p class="muted">{message('nodeRecordedHint')}</p>
              {/if}
              <form onsubmit={(event) => { event.preventDefault(); void inspectNode(); }} onchange={rememberAnswers}>
                <label><span>{message('nodeTarget')}</span><select bind:value={nodeTargetKind}><option value="remote">{message('nodeRemote')}</option>{#if nodeCapabilities?.sameHostSupported}<option value="same-host">{message('nodeSameHost')}</option>{/if}</select></label>
                {#if nodeTargetKind === 'remote'}
                  <div class="form-grid"><label><span>{message('nodeHost')}</span><input bind:value={nodeHost} required autocomplete="off" /></label><label><span>{message('nodePort')}</span><input type="number" bind:value={nodePort} min="1" max="65535" required /></label></div>
                  <label><span>{message('nodeUsername')}</span><input bind:value={nodeUsername} required autocomplete="username" /></label>
                  <label><span>{message('nodeAuthentication')}</span><select bind:value={nodeAuthentication}><option value="agent">{message('nodeAgent')}</option><option value="private-key">{message('nodePrivateKey')}</option><option value="password">{message('nodePassword')}</option></select></label>
                  {#if nodeAuthentication === 'password'}<label><span>{message('nodePassword')}</span><input type="password" bind:value={nodePassword} required={!nodePasswordStored} autocomplete="current-password" />{#if nodePasswordStored}<small class="muted">{message('secretAlreadySaved')}</small>{/if}</label>{:else if nodeAuthentication === 'private-key'}<label><span>{message('nodePrivateKey')}</span><textarea bind:value={nodePrivateKey} required={!nodePrivateKeyStored} autocomplete="off"></textarea>{#if nodePrivateKeyStored}<small class="muted">{message('secretAlreadySaved')}</small>{/if}</label><label><span>{message('nodeKeyPassphrase')}</span><input type="password" bind:value={nodeKeyPassphrase} autocomplete="off" /></label>{/if}
                {/if}
                <label><span>{message('nodeSudoPassword')}</span><input type="password" bind:value={nodeSudoPassword} autocomplete="off" /></label>
                <label><span>{message('localBootstrapDataDirectory')}</span><input bind:value={localBootstrapDataDirectory} required /></label>
                {#if nodeTargetKind === 'remote'}
                  <div class="actions"><button type="button" class="secondary" onclick={() => void probeNode()} disabled={nodeBusy}>{message('nodeProbe')}</button></div>
                {/if}
                {#if nodeProbe}<p class="inline-notice">{message('nodeFingerprint')}: <code>{nodeProbe.fingerprint}</code> <button type="button" onclick={() => void trustNode()} disabled={nodeBusy}>{message('nodeTrust')}</button></p>{/if}
                <div class="actions"><button type="submit" disabled={nodeBusy}>{message('nodeInspect')}</button></div>
              </form>

              {#if nodeInspection}
                <dl class="credential-metadata">
                  <div><dt>{message('nodeOperatingSystem')}</dt><dd>{nodeInspection.report.operatingSystem} / {nodeInspection.report.architecture}</dd></div>
                  <div><dt>{message('nodeCapacity')}</dt><dd>{formatNumber(locale, nodeInspection.report.capacity.memoryMi)} MiB · {formatNumber(locale, nodeInspection.report.capacity.diskGi)} GiB</dd></div>
                  <div><dt>{message('nodeAssessment')}</dt><dd>
                    {#if nodeInspection.assessment.ready}
                      {message('nodeReady')}
                    {:else}
                      <!-- One finding per line, in words. The raw codes said nothing
                           to the operator about what was actually in the way. -->
                      <ul class="blocker-list">
                        {#each nodeInspection.assessment.blockers as blocker (blocker.code)}
                          <li>{blockerLabel(blocker.code)} <code>{blocker.code}</code></li>
                        {/each}
                      </ul>
                    {/if}
                  </dd></div>
                </dl>
                {#if !nodeInspection.assessment.ready && nodeInspection.assessment.blockers.some(b => b.code === 'installation.kubernetes.foreign' || b.code === 'installation.data.foreign')}
                  <!-- Removal runs the k3s uninstaller and rm -rf over fixed paths plus
                       the data directory (internal/launcher/clean_node.go). The list
                       below is that command spelled out; keep the two in step. -->
                  <section class="foreign-install" aria-labelledby="foreign-install-title">
                    <h3 id="foreign-install-title">{message('foreignInstallFound')}</h3>
                    <p>{message('foreignInstallEffectTitle')}</p>
                    <ul>
                      {#if nodeInspection.assessment.blockers.some(b => b.code === 'installation.kubernetes.foreign')}
                        <li>{message('foreignInstallEffectK3S')}</li>
                      {/if}
                      <li>
                        {message('foreignInstallEffectPaths')}
                        <ul>
                          {#each foreignRemovalPaths as path (path)}<li><code>{path}</code></li>{/each}
                        </ul>
                      </li>
                    </ul>
                    <p class="inline-error">{message('foreignInstallIrreversible')}</p>
                    <div class="actions">
                      <button class="danger" onclick={() => void cleanNode()} disabled={cleanNodeBusy}>
                        {cleanNodeBusy ? message('foreignInstallRemoving') : message('foreignInstallRemove')}
                      </button>
                    </div>
                  </section>
                {/if}
              {/if}

              {#if activeProfile.deploymentMode === 'local-public'}
                <!-- The launcher will not change a router, so the rules are named
                     exactly and confirmed by hand. Stated here, in the stage about
                     preparing the machine, rather than as a refusal at install time. -->
                <section class="handoff-steps">
                  <h3 id="router-forwarding-title">{message('localPublicRouterTitle')}</h3>
                  <p class="muted">{message('localPublicRouterDescription')}</p>
                  <ul>
                    <li><code>80/tcp → 80/tcp</code> — {message('localPublicRouterHTTP')}</li>
                    <li><code>443/tcp → 443/tcp</code> — {message('localPublicRouterHTTPS')}</li>
                    <li><code>10000/udp → 10000/udp</code> — {message('localPublicRouterJitsi')}</li>
                  </ul>
                  <p class="muted">{message('localPublicRouterNoAutomation')}</p>
                  <form onchange={rememberAnswers}>
                    <label><span>{message('localPublicDNSProvider')}</span><input value="Hetzner DNS (DNS-01)" readonly /></label>
                    <label><span>{message('localPublicDNSToken')}</span><input type="password" bind:value={localPublicDNSToken} required={!dnsTokenStored} autocomplete="off" />{#if dnsTokenStored}<small class="muted">{message('secretAlreadySaved')}</small>{/if}</label>
                    <p class="muted">{message('localPublicDDNS')}</p>
                    <label class="check"><input type="checkbox" bind:checked={localPublicRouterAcknowledged} required /><span>{message('localPublicRouterAcknowledge')}</span></label>
                  </form>
                  <ul><li>{message('localPublicMailWarning')}</li><li>{message('localPublicJitsiWarning')}</li></ul>
                </section>
              {/if}

              <div class="advanced">
                <button type="button" class="secondary" aria-expanded={advancedMachine} onclick={() => advancedMachine = !advancedMachine}>{advancedMachine ? message('advancedHide') : message('advancedShow')}</button>
                {#if advancedMachine}
                  <div class="advanced-body">
                    <p class="muted">{message('advancedHint')}</p>
                    <label><span>{message('localBootstrapNodeName')}</span><input bind:value={localBootstrapNodeName} required onchange={rememberAnswers} /></label>
                    {#if activeProfile.deploymentMode === 'local-lan'}
                      <label class="check"><input type="checkbox" bind:checked={localBootstrapManageDNS} onchange={rememberAnswers} /><span>{message('localBootstrapManageDNS')}</span></label>
                    {/if}
                    {#if nodeTargetKind === 'remote'}
                      <div class="actions">
                        <button class="secondary" onclick={() => void planNodeSSHKey()} disabled={nodeBusy || sshKeyPlan !== null}>
                          {#if sshKeyPlan}✓ {message('nodeSSHKeyPlanned')}{:else}{message('nodePlanSSHKey')}{/if}
                        </button>
                      </div>
                    {/if}
                  </div>
                {/if}
              </div>
            </section>
          {/if}

          {#if activeStep === 'machine' && rentsMachine}
            <section class="card hetzner-card" aria-labelledby="hetzner-title">
              <p class="eyebrow">{message('hetznerEyebrow')}</p>
              <h2 id="hetzner-title">{message('hetznerTitle')}</h2>
              <p class="muted">{message('hetznerDescription')}</p>
              {#if hetznerError}<p class="inline-error" role="alert">{hetznerError}</p>{/if}

              <form onsubmit={(event) => { event.preventDefault(); void validateHetznerToken(); }}>
                <label><span>{message('hetznerToken')}</span><input type="password" bind:value={hetznerTokenValue} required={!hetznerTokenStored} autocomplete="off" />{#if hetznerTokenStored}<small class="muted">{message('secretAlreadySaved')}</small>{/if}</label>
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
                <!-- The domain is the one already given; this only reads the
                     project and checks the delegation against it. -->
                <div class="actions"><button type="button" onclick={() => void inspectHetznerProject()} disabled={hetznerBusy}>{message('hetznerInspect')}</button></div>
              {/if}

              {#if hetznerProject?.inventory}
                <section class="capability-preview">
                  <h3 id="hetzner-inventory-title">{message('hetznerInventory')}</h3>
                  <p class="muted">{message('hetznerInspectedAt')}: {formatDateTime(locale, hetznerProject.inspectedAt)}</p>
                  <ul class="hetzner-inventory" data-testid="hetzner-inventory">
                    {#each hetznerProject.inventory.findings ?? [] as finding (finding.expectation?.kind + '/' + finding.expectation?.name)}
                      <li class:decision={finding.requiresDecision}>
                        <code>{finding.expectation?.kind}</code> <strong>{finding.expectation?.name}</strong>
                        <span class="badge">{hetznerOwnershipLabel(finding.ownership)}</span>
                        {#if finding.match?.detail}<span class="muted">{finding.match.detail}</span>{/if}
                        {#if finding.ownership === 'adoptable' && finding.match?.providerId}
                          <label class="check"><input type="checkbox" checked={hetznerAdoptions.includes(finding.match.providerId)} onchange={() => { const id = finding.match?.providerId; if (id) hetznerAdoptions = hetznerAdoptions.includes(id) ? hetznerAdoptions.filter((candidate) => candidate !== id) : [...hetznerAdoptions, id]; }} /><span>{message('hetznerAdoptSelected')}</span></label>
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
                <section class="capability-preview">
                  <h3 id="hetzner-capacity-title">{message('hetznerCapacity')}</h3>
                  <p class="muted">{message('hetznerRequirement')}: {formatNumber(locale, hetznerPresets.requirement?.memoryGb)} GB · {formatNumber(locale, hetznerPresets.requirement?.volumeGb)} GB</p>
                  <ul class="hetzner-presets" data-testid="hetzner-presets">
                    {#each hetznerPresets.presets ?? [] as preset (preset.tier)}
                      <li>
                        <label class="check"><input type="radio" name="hetzner-preset" value={preset.tier} checked={hetznerTier === preset.tier} onchange={() => { hetznerTier = (preset.tier ?? 'recommended') as HetznerPresetTier; hetznerServerType = preset.serverType ?? ''; hetznerVolumeGb = preset.volumeGb ?? 0; rememberAnswers(); }} /><span><strong>{hetznerPresetLabel(preset.tier)}</strong> · {preset.serverType} · {formatNumber(locale, preset.memoryGb)} GB · {formatNumber(locale, preset.volumeGb)} GB · {formatCurrency(locale, preset.cost?.totalMonthlyEur, preset.cost?.currency ?? 'EUR')}</span></label>
                        {#if preset.fits === false}<p class="muted">{message('hetznerPresetTooSmall')}</p>{/if}
                        {#if preset.available === false}<p class="muted">{message('hetznerPresetUnavailable')}</p>{/if}
                      </li>
                    {/each}
                  </ul>
                  <label><span>{message('hetznerLocation')}</span>
                    <select bind:value={hetznerLocation} onchange={() => { rememberAnswers(); void loadHetznerPresets(); }}>
                      {#each hetznerPresets.locations ?? [] as location (location)}<option value={location}>{location}</option>{/each}
                    </select>
                  </label>
                  <p class="muted">{message('hetznerPricesObservedAt')}: {formatDateTime(locale, hetznerPresets.observedAt)}</p>
                  <div class="actions"><button type="button" onclick={() => void planHetznerInfrastructure()} disabled={hetznerBusy || !hetznerProject?.inventory || !adminEmail.includes('@')}>{message('hetznerPlanBuild')}</button></div>
                </section>
              {/if}

              <div class="advanced">
                <button type="button" class="secondary" aria-expanded={advancedMachine} onclick={() => advancedMachine = !advancedMachine}>{advancedMachine ? message('advancedHide') : message('advancedShow')}</button>
                {#if advancedMachine}
                  <div class="advanced-body">
                    <p class="muted">{message('advancedHint')}</p>
                    <label class="check"><input type="radio" name="hetzner-preset" value="advanced" checked={hetznerTier === 'advanced'} onchange={() => { hetznerTier = 'advanced'; rememberAnswers(); }} /><span>{message('hetznerAdvancedHint')}</span></label>
                    {#if hetznerTier === 'advanced' && hetznerPresets}
                      <div class="form-grid">
                        <label><span>{message('hetznerServerType')}</span>
                          <select bind:value={hetznerServerType} onchange={rememberAnswers}>
                            {#each hetznerPresets.offerings ?? [] as offering (offering.name)}<option value={offering.name}>{offering.name} · {formatNumber(locale, offering.memoryGb)} GB · {formatCurrency(locale, offering.monthlyEur)}</option>{/each}
                          </select>
                        </label>
                        <label><span>{message('hetznerVolume')}</span><input type="number" min="10" max="10240" step="10" bind:value={hetznerVolumeGb} onchange={rememberAnswers} /></label>
                      </div>
                    {/if}
                    <!-- Fetched automatically when this stage opens; shown so the
                         pinned versions are inspectable, not so anyone has to ask
                         for them. -->
                    {#if hetznerToolchain}
                      <dl class="credential-metadata">
                        <div><dt>OpenTofu</dt><dd>{hetznerToolchain.openTofuVersion}</dd></div>
                        <div><dt>hcloud</dt><dd>{hetznerToolchain.hcloudProviderVersion}</dd></div>
                        <div><dt>{message('hetznerToolchainTitle')}</dt><dd><span class="badge">{hetznerToolchain.ready ? message('hetznerToolchainReady') : message('hetznerToolchainPending')}</span></dd></div>
                        {#if hetznerWorkspace}<div><dt>{message('hetznerWorkspace')}</dt><dd>{hetznerWorkspace.isolated ? message('hetznerWorkspaceIsolated') : ''} · {formatNumber(locale, hetznerWorkspace.backups ?? 0)}</dd></div>{/if}
                      </dl>
                      {#if hetznerToolchain.reasonKey === 'toolchain-artifacts-unavailable'}<p class="muted">{message('hetznerToolchainUnavailable')}</p>{/if}
                      {#if !hetznerToolchain.ready}<div class="actions"><button type="button" class="secondary" onclick={() => void acquireHetznerToolchain()} disabled={hetznerBusy}>{message('installerFilesRetry')}</button></div>{/if}
                    {/if}
                  </div>
                {/if}
              </div>
            </section>
          {/if}

          {#if activeStep === 'install'}
            <section class="card install-card" aria-labelledby="install-title">
              <p class="eyebrow">{message('localBootstrapEyebrow')}</p>
              <h2 id="install-title">{message('stepInstallTitle')}</h2>
              <p class="muted">{message('stepInstallSummary')}</p>

              <!-- Fetched, not asked for. The version was decided when the
                   repository was written, and "which files does that need" has
                   no second answer for an operator to give. -->
              <p class="facility-state" aria-live="polite">
                <span aria-hidden="true">{installerFilesReady ? '✓' : bootstrapAssetError ? '!' : '…'}</span>
                {#if bootstrapAssetBusy}{message('installerFilesWorking').replace('{release}', release)}
                {:else if installerFilesReady}{message('installerFilesReady').replace('{release}', bootstrapAssets?.release ?? release)}
                {:else}{message('installerFilesFailed')}{/if}
              </p>
              {#if bootstrapAssetError}
                <p class="inline-error" role="alert">{bootstrapAssetError === 'bootstrap_asset_release_unavailable' ? message('bootstrapAssetUnavailable') : bootstrapAssetError}</p>
                <div class="actions"><button type="button" class="secondary" onclick={() => void ensureInstallerFiles()} disabled={bootstrapAssetBusy}>{message('installerFilesRetry')}</button></div>
              {/if}
              {#if bootstrapAssets}
                <details class="asset-detail">
                  <summary>{message('installerFilesWhat')}</summary>
                  <dl class="credential-metadata">{#each bootstrapAssets.assets as asset (asset.id)}<div><dt>{asset.id}</dt><dd>{asset.destination} · {asset.state} · <code>{asset.sha256.slice(0, 16)}…</code></dd></div>{/each}</dl>
                  <p class="muted">{message('offlineBundleFuture')}</p>
                </details>
              {/if}

              {#if rentsMachine}
                {#if hetznerPlan}
                  <section class="capability-preview">
                    <p class="eyebrow">{message('planTitle')}</p>
                    <h3 id="hetzner-plan-title">{message('hetznerPlanTitle')}</h3>
                    <dl class="credential-metadata">
                      <div><dt>{message('hetznerServerType')}</dt><dd>{hetznerPlan.changePlan?.choice?.serverType} · {hetznerPlan.changePlan?.choice?.location}</dd></div>
                      <div><dt>{message('hetznerVolume')}</dt><dd>{formatNumber(locale, hetznerPlan.changePlan?.choice?.volumeGb)} GB</dd></div>
                      <div><dt>{message('hetznerMonthlyCost')}</dt><dd data-testid="hetzner-cost">{hetznerPlanCost(hetznerPlan.changePlan)}</dd></div>
                      <div><dt>{message('digest')}</dt><dd data-testid="plan-digest"><code>{hetznerPlan.changePlan?.digest}</code></dd></div>
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
                      <div class="actions"><button type="button" onclick={() => void approveHetznerPlan()} disabled={busy || !installerFilesReady || run?.state === 'running'}>{message('approve')}</button></div>
                    {/if}
                  </section>
                {/if}

                {#if hetznerTemporaryAccess}
                  <section class="capability-preview" data-testid="hetzner-temporary-access">
                    <h3 id="hetzner-access-title">{message('hetznerAccessTitle')}</h3>
                    <p class="muted">{message('hetznerAccessDescription')}</p>
                    <dl class="credential-metadata">
                      <div><dt>{message('hetznerAccessState')}</dt><dd><span class="badge">{hetznerTemporaryAccess.open ? message('hetznerAccessOpen') : message('hetznerAccessClosed')}</span></dd></div>
                      <div><dt>{message('hetznerAccessScope')}</dt><dd>{hetznerTemporaryAccess.scope?.scoped ? (hetznerTemporaryAccess.scope?.sources ?? []).join(', ') : message('hetznerAccessUnscoped')}</dd></div>
                      <div><dt>{message('hetznerAccessReason')}</dt><dd>{hetznerAccessReasonLabel(hetznerTemporaryAccess.scope?.reasonKey)}</dd></div>
                    </dl>
                    {#if hetznerTemporaryAccess.open}
                      <label><span>{message('hetznerAccessAddress')}</span><input type="text" bind:value={hetznerOperatorAddress} placeholder="198.51.100.7" onchange={rememberAnswers} /></label>
                      <p class="muted">{message('hetznerAccessAddressHint')}</p>
                      <div class="actions"><button type="button" class="secondary" onclick={() => void narrowHetznerTemporaryAccess()} disabled={hetznerBusy}>{message('hetznerAccessNarrow')}</button></div>
                    {/if}
                  </section>
                {/if}
              {:else}
                <p class="muted">{message('localBootstrapDescription')}</p>
                <!-- The missing prerequisite is stated before the form rather than
                     only as the refusal that follows a click. Said up front it is
                     guidance; said afterwards it reads as a failure. -->
                {#if !overlayIdentity}<p class="inline-notice">{message('localBootstrapOverlayRequired')}</p>{/if}
                {#if localBootstrapError}<p class="inline-error" role="alert">{localBootstrapErrorMessage(localBootstrapError)}</p>{/if}
                <dl class="credential-metadata">
                  <div><dt>{message('capabilityDomain')}</dt><dd><code>{overlayIdentity?.domain || domain}{environmentExtension}</code></dd></div>
                  <div><dt>{message('capabilityRelease')}</dt><dd><code>{overlayIdentity?.release || release}</code></dd></div>
                  <div><dt>{message('localBootstrapDataDirectory')}</dt><dd><code>{localBootstrapDataDirectory}</code></dd></div>
                </dl>

                <div class="advanced">
                  <button type="button" class="secondary" aria-expanded={advancedInstall} onclick={() => advancedInstall = !advancedInstall}>{advancedInstall ? message('advancedHide') : message('advancedShow')}</button>
                  {#if advancedInstall}
                    <div class="advanced-body">
                      <p class="muted">{message('advancedHint')}</p>
                      <label><span>{message('localBootstrapSecrets')}</span><textarea bind:value={localBootstrapSecrets} autocomplete="off" placeholder="apiVersion: v1&#10;kind: Secret&#10;…"></textarea></label>
                      {#if nodeTargetKind === 'same-host'}<label><span>{message('nodeSudoPassword')}</span><input type="password" bind:value={nodeSudoPassword} autocomplete="off" /></label>{/if}
                    </div>
                  {/if}
                </div>

                <div class="actions"><button type="button" onclick={() => void planLocalBootstrap()} disabled={localBootstrapBusy || !installerFilesReady}>{message('localBootstrapReview')}</button></div>

                {#if installPlan}
                  <section class="capability-preview">
                    <p class="eyebrow">{message('capabilityPreview')}</p>
                    <h3 id="install-plan-title">{message('planTitle')}</h3>
                    <dl>
                      <div><dt>{message('digest')}</dt><dd data-testid="plan-digest"><code>{installPlan.digest}</code></dd></div>
                      <div><dt>{message('effect')}</dt><dd>{installPlan.effects?.map((entry) => planItemLabel(entry.code)).join('; ') || message('effect')}</dd></div>
                      <div><dt>{message('noRisk')}</dt><dd>{installPlan.risks?.map((entry) => planItemLabel(entry.code)).join('; ') || message('noRisk')}</dd></div>
                      {#if installPlan.preconditions.bootstrapRelease}<div><dt>{message('capabilityRelease')}</dt><dd>{installPlan.preconditions.bootstrapRelease}</dd></div>{/if}
                      {#if installPlan.preconditions.overlayCommit}<div><dt>{message('localBootstrapOverlayCommit')}</dt><dd><code>{installPlan.preconditions.overlayCommit}</code></dd></div>{/if}
                      {#if installPlan.preconditions.dataDirectory}<div><dt>{message('localBootstrapDataDirectory')}</dt><dd><code>{installPlan.preconditions.dataDirectory}</code></dd></div>{/if}
                    </dl>
                    <p class="muted">{message('installReadyHint')}</p>
                    <div class="actions"><button onclick={() => void approve(installPlan, 'install')} disabled={busy || run?.state === 'running'}>{message('approve')}</button></div>
                  </section>
                {/if}
              {/if}

              {#if runPurpose === 'install' && run}
                <p class="inline-notice" aria-live="polite">{run.state === 'verified' ? message('installDone') : message('installWatching')}</p>
              {/if}
            </section>
          {/if}

          {#if activeStep === 'handoff'}
            <!-- Six things in one order. Every one of them used to be a button of
                 its own with nothing to say which came first, so the stage now
                 offers exactly the next one and reports the rest as evidence. -->
            <section class="card handoff-card" aria-labelledby="handoff-title">
              <p class="eyebrow">{message('handoffEyebrow')}</p>
              <h2 id="handoff-title">{activeProfile.deploymentMode === 'local-public' ? message('localPublicHandoffTitle') : message('handoffTitle')}</h2>
              <p class="muted">{activeProfile.deploymentMode === 'local-public' ? message('localPublicHandoffDescription') : message('handoffDescription')}</p>
              {#if handoffError}<p class="inline-error" role="alert">{handoffError}</p>{/if}

              {#if vaultStatus?.state !== 'unlocked'}
                <p class="inline-notice">{message('handoffUnlockFirst')}</p>
              {:else if nextHandoffAction}
                <section class="next-action">
                  <p class="eyebrow">{message('handoffNextHeading')}</p>
                  <h3 id="handoff-next-title">{message(nextHandoffAction.labelKey)}</h3>
                  {#if nextHandoffAction.stepName === 'private-network'}
                    <label><span>{message('handoffBaseDomain')}</span><input bind:value={handoffBaseDomain} required placeholder="smallworlds.internal" onchange={rememberAnswers} /></label>
                  {/if}
                  {#if nextHandoffAction.stepName === 'launcher-enrolled' && tailscaleOffer}
                    <p class="muted">{tailscaleOffer.detected ? message('handoffTailscaleDetected') : message('handoffTailscaleAbsent')} {#if !tailscaleOffer.detected}<a href={tailscaleOffer.acquisition.manualInstructionsUrl} target="_blank" rel="noreferrer">{message('handoffTailscaleManual')}</a>{/if}</p>
                  {/if}
                  <div class="actions"><button type="button" onclick={() => void nextHandoffAction.act()} disabled={handoffBusy}>{message(nextHandoffAction.labelKey)}</button></div>
                </section>
              {:else}
                <p class="inline-notice">{message('handoffComplete')}</p>
              {/if}
              {#if deviceTrustFingerprint}<p class="inline-notice">{message('handoffDeviceTrustFingerprint')}: <code>{deviceTrustFingerprint}</code></p>{/if}

              {#if handoffAssessment}
                <section class="handoff-steps">
                  <h3>{message('handoffStepsTitle')}</h3>
                  <ul class="handoff-checklist">
                    {#each handoffAssessment.steps as step (step.name)}
                      <li class:complete={step.complete}><span aria-hidden="true">{step.complete ? '✓' : '○'}</span> {handoffStepLabel(step.name)}</li>
                    {/each}
                  </ul>
                </section>
                <section class="handoff-limitations">
                  <h3>{message('handoffLimitations')}</h3>
                  <ul>{#each handoffAssessment.limitations as limitation (limitation)}<li>{limitation}</li>{/each}</ul>
                </section>
              {/if}
            </section>
          {/if}

          {#if activeStep === 'protect'}
            <section class="card offsite-card" aria-labelledby="offsite-title">
              <p class="eyebrow">{message('offsiteEyebrow')}</p>
              <h2 id="offsite-title">{message('offsiteTitle')}</h2>
              <p class="muted">{message('offsiteDescription')}</p>
              {#if offsiteError}<p class="inline-error" role="alert">{offsiteError}</p>{/if}
              <form onsubmit={(event) => { event.preventDefault(); void inspectOffsiteDestination(); }} onchange={rememberAnswers}>
                <div class="form-grid"><label><span>{message('offsiteEndpoint')}</span><input type="url" bind:value={offsiteEndpoint} required placeholder="https://s3.eu-central-003.backblazeb2.com" /></label><label><span>{message('offsiteRegion')}</span><input bind:value={offsiteRegion} required placeholder="eu-central-003" autocomplete="off" /></label></div>
                <label><span>{message('offsiteBucket')}</span><input bind:value={offsiteBucket} required placeholder="community-backups" autocomplete="off" /></label>
                <div class="form-grid"><label><span>{message('offsiteAccessKey')}</span><input bind:value={offsiteAccessKey} required={!offsiteKeysStored} autocomplete="off" /></label><label><span>{message('offsiteSecretKey')}</span><input type="password" bind:value={offsiteSecretKey} required={!offsiteKeysStored} autocomplete="off" />{#if offsiteKeysStored}<small class="muted">{message('secretAlreadySaved')}</small>{/if}</label></div>
                <div class="actions"><button type="submit" disabled={offsiteBusy}>{message('offsiteInspect')}</button></div>
              </form>
              {#if offsiteStatus?.destination?.bucket}
                <dl class="credential-metadata"><div><dt>{message('offsiteBucket')}</dt><dd>{offsiteStatus.destination.bucket} · {offsiteStatus.destination.region}</dd></div><div><dt>{message('offsiteVersioning')}</dt><dd>{offsiteVersioningLabel(offsiteStatus.versioning)}</dd></div><div><dt>{message('offsiteFingerprint')}</dt><dd><code>{offsiteStatus.accessKeyFingerprint}</code></dd></div></dl>
                {#if offsiteStatus.requiresAcknowledgement}<label class="check"><input type="checkbox" bind:checked={offsiteAcknowledge} /><span>{message('offsiteAcknowledge')}</span></label>{/if}
                <div class="actions"><button type="button" onclick={() => void planOffsiteProtection()} disabled={offsiteBusy || (offsiteStatus.requiresAcknowledgement && !offsiteAcknowledge)}>{message('offsitePlanReview')}</button></div>
              {/if}
              {#if offsitePlan}
                <section class="capability-preview">
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

              <!-- The single most important file in the whole system, asked for in
                   the stage about not losing things rather than hidden in a menu. -->
              <section class="capability-preview">
                <h3 id="protect-recovery-title">{message('recoveryExport')}</h3>
                <p class="muted">{message('recoveryExportDescription')}</p>
                {#if recoveryError}<p class="inline-error" role="alert">{recoveryError}</p>{/if}
                {#if recoveryNotice}<p class="inline-notice" aria-live="polite">{recoveryNotice}</p>{/if}
                <form onsubmit={(event) => { event.preventDefault(); void exportRecoveryBundle(); }}>
                  <label>
                    <span>{message('recoveryPassphrase')}</span>
                    <input type="password" bind:value={recoveryPassphrase} minlength="12" autocomplete="new-password" placeholder={message('recoveryPassphraseHint')} />
                  </label>
                  <div class="actions"><button type="submit" disabled={recoveryBusy || (!recoveryPassphrase && !recoveryRecipients)}>{message('recoveryDownload')}</button></div>
                </form>
              </section>
            </section>
          {/if}

          {#if activeStep === 'finish'}
            <section class="card finish-card" aria-labelledby="finish-title">
              <p class="eyebrow">{message('journeyProgress')}</p>
              <h2 id="finish-title">{message('stepFinishTitle')}</h2>
              <p class="muted">{message('stepFinishSummary')}</p>

              <!-- Warnings are not hidden to make the screen look finished. -->
              {#if steps.some((step) => !step.done)}
                <h3>{message('finishOutstanding')}</h3>
                <ul class="handoff-checklist">
                  {#each steps.filter((step) => !step.done && step.id !== 'finish') as step (step.id)}
                    <li><span aria-hidden="true">○</span> {message(step.titleKey)}</li>
                  {/each}
                </ul>
                {#if !protectionVerified}<p class="inline-error">{message('finishProtectionMissing')}</p>{/if}
              {:else}
                <p class="inline-notice">{message('finishHealthy')}</p>
              {/if}

              {#if handoffAssessment?.complete && handoffAssessment.consoleHandoffUrl}
                <dl class="credential-metadata">
                  <div><dt>{message('handoffConsoleUrl')}</dt><dd data-testid="console-handoff-url"><a href={handoffAssessment.consoleHandoffUrl}>{handoffAssessment.consoleHandoffUrl}</a></dd></div>
                </dl>
                <p class="muted">{message('finishConsoleHint')}</p>
              {/if}
            </section>
          {/if}

          <!-- Forwards and backwards on every stage, in the same place, so
               revisiting an earlier answer never means hunting for the way back. -->
          <div class="step-navigation">
            <button type="button" class="secondary" onclick={goToPreviousStep} disabled={currentStepIndex <= 0}>{message('stepBack')}</button>
            <button type="button" onclick={goToNextStep} disabled={currentStepIndex >= steps.length - 1}>{message('continue')}</button>
          </div>
        </div>
        {/if}

        {#if activeTab === 'activity'}
        <!-- One record for the whole installation, wherever the action was
             started from. It used to be a footer on every stage that only ever
             showed what this browser session happened to watch. -->
        <div id="panel-activity" role="tabpanel" aria-labelledby="tab-activity" tabindex="0">
          <section aria-labelledby="activity-title">
            <p class="eyebrow">{message('activity')}</p>
            <h2 id="activity-title">{message('tabActivity')}</h2>
            <p class="muted">{message('tabActivitySummary')}</p>
            {#if run}
              <div class="run-status" class:verified={run.state === 'verified'}>
                <span class="status-icon" aria-hidden="true">{run.state === 'verified' ? '✓' : '•'}</span>
                <span>{runLabel(run.state)}</span>
                <small>{run.currentCheckpoint || message('running')}</small>
                {#if run.state === 'running' && run.cancellationState === 'not-requested'}<button class="secondary" onclick={() => void cancelRun()} disabled={busy}>{message('cancel')}</button>{/if}
              </div>
            {/if}
            {#if activities.length === 0}
              <p class="muted">{message('activityEmpty')}</p>
            {:else}
              <ol class="timeline">
                {#each activities as activity (activity.id)}
                  <li>
                    <span aria-hidden="true"></span>
                    <span class="timeline-label">{activityLabel(activity.type)} <code>{activity.type}</code></span>
                    <time datetime={activity.occurredAt}>{formatDateTime(locale, activity.occurredAt)}</time>
                  </li>
                {/each}
              </ol>
            {/if}
            <p class="muted">{message('activityRedactionNote')}</p>
          </section>
        </div>
        {/if}

        {#if activeTab === 'manage'}
        <div id="panel-manage" role="tabpanel" aria-labelledby="tab-manage" tabindex="0">
          <section class="card" aria-labelledby="manage-details-title">
            <p class="eyebrow">{message('tabManage')}</p>
            <h2 id="manage-details-title">{message('manageDetails')}</h2>
            <dl class="credential-metadata">
              <div><dt>{message('profileName')}</dt><dd>{activeProfile.name}</dd></div>
              <div><dt>{message('deploymentMode')}</dt><dd>{activeProfile.deploymentMode}</dd></div>
              <div><dt>{message('recoveryClusterId')}</dt><dd><code>{activeProfile.id}</code></dd></div>
            </dl>
            <div class="actions"><button class="secondary" onclick={showEditProfile}>{message('editProfile')}</button></div>
          </section>

          <!-- The safe reports what it holds; it does not ask for anything. Each
               stage collects the secret it needs where that secret makes sense,
               so there is no field here that the operator has to guess at. -->
          <section class="card vault-card" aria-labelledby="credentials-title">
            <div class="vault-heading">
              <div>
                <p class="eyebrow">{message('vaultTitle')}</p>
                <h2 id="credentials-title">{message('manageCredentials')}</h2>
              </div>
              <span class:unlocked={vaultStatus?.state === 'unlocked'} class="badge">
                {vaultStatus?.state === 'unlocked' ? message('vaultUnlocked') : message('vaultLocked')}
              </span>
            </div>
            <p class="muted">{message('vaultNoInput')}</p>
            {#if vaultError}<p class="inline-error" role="alert">{vaultError}</p>{/if}
            {#if credentials.length > 0}
              {#each credentials as credential (credential.kind)}
                <dl class="credential-metadata">
                  <div><dt>{credentialLabel(credential.kind)}</dt><dd><span class="badge">{credential.present ? message('credentialPresent') : message('noCredential')}</span></dd></div>
                  <div><dt>{message('credentialSource')}</dt><dd>{credential.source === 'operator' ? message('sourceOperator') : credential.source}</dd></div>
                  <div><dt>{message('credentialExpires')}</dt><dd>{credential.expiresAt}</dd></div>
                  <div><dt>{message('rotationStatus')}</dt><dd>{rotationLabel(credential.rotationStatus)}</dd></div>
                </dl>
              {/each}
              <!-- Only the orphaned kind can be cleared from here. Removing a secret
                   a stage depends on belongs to that stage, not to this overview. -->
              {#if secretStored('git-provider-token')}
                <div class="actions"><button type="button" class="danger" onclick={() => void removeCredential()} disabled={vaultBusy}>{message('removeCredential')}</button></div>
              {/if}
            {:else}
              <p class="muted">{message('noCredential')}</p>
            {/if}
          </section>

          <section class="card recovery-card" aria-labelledby="recovery-title">
            <p class="eyebrow">{message('recoveryEyebrow')}</p>
            <h2 id="recovery-title">{message('recoveryTitle')}</h2>
            <p class="muted">{message('recoveryDescription')}</p>
            {#if recoveryError}<p class="inline-error" role="alert">{recoveryError}</p>{/if}
            {#if recoveryNotice}<p class="inline-notice" aria-live="polite">{recoveryNotice}</p>{/if}

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
              {#if recoveryCredentialMode === 'identity'}
                <label>
                  <span>{message('recoveryAgeIdentity')}</span>
                  <textarea bind:value={recoveryIdentity} rows="3" autocomplete="off"></textarea>
                </label>
              {/if}
              <div class="actions"><button type="submit" disabled={recoveryBusy || !recoveryBundle || (recoveryCredentialMode === 'passphrase' ? recoveryPassphrase.length < 12 : !recoveryIdentity)}>{message('recoveryPreview')}</button></div>
            </form>

            {#if recoveryPreview}
              <section class="recovery-preview">
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

          <!-- A rehearsal that changes nothing. It answers "is this console
               working at all", which nobody asks until something else has
               already gone wrong — so it lives with the diagnostics rather than
               in the middle of setting an installation up. -->
          <section class="card" aria-labelledby="diagnostics-title">
            <p class="eyebrow">{message('manageDiagnostics')}</p>
            <h2 id="diagnostics-title">{message('task')}</h2>
            <p class="muted">{message('taskDescription')}</p>
            {#if !selfTestPlan}
              <div class="actions"><button class="secondary" onclick={() => void createSelfTestPlan()} disabled={selfTestBusy}>{message('inspectPlan')}</button></div>
            {:else}
              <dl>
                <div><dt>{message('digest')}</dt><dd data-testid="plan-digest"><code>{selfTestPlan.digest}</code></dd></div>
                <div><dt>{message('effect')}</dt><dd>{selfTestPlan.effects?.map((entry) => planItemLabel(entry.code)).join('; ') || message('effect')}</dd></div>
                <div><dt>{message('noRisk')}</dt><dd>{message('noRisk')}</dd></div>
              </dl>
              <div class="actions"><button onclick={() => void approve(selfTestPlan, 'self-test')} disabled={busy || run?.state === 'running'}>{message('approve')}</button></div>
            {/if}
          </section>

          <!-- Only offered once there is something out there to shut down.
               Before that it is an invitation to worry about a machine that
               does not exist. -->
          <section class="card retire-card" aria-labelledby="retire-title">
            <p class="eyebrow">{decommissionMessage('eyebrow')}</p>
            <h2 id="retire-title">{message('retireTitle')}</h2>
            <p class="muted">{message('retireDescription')}</p>
            {#if decommissionError}<p class="inline-error" role="alert">{decommissionError}</p>{/if}
            {#if !clusterEstablished}
              <p class="muted">{message('retireNotYet')}</p>
              <div class="actions"><button type="button" class="secondary" onclick={() => activeProfile && void forgetProfile(activeProfile)} disabled={decommissionBusy}>{decommissionMessage('forget')}</button></div>
            {:else}
              <div class="actions"><button type="button" class="secondary" aria-expanded={showRetire} onclick={() => showRetire = !showRetire}>{showRetire ? message('retireHide') : message('retireShow')}</button></div>
              {#if showRetire}
                <section class="decommission-card">
                  <h3 id="decommission-title">{decommissionMessage('preserveTitle')}</h3>
                  <p class="muted">{decommissionMessage('preserveDescription')}</p>
                  {#if !decommissionPlan}
                    <div class="actions"><button type="button" class="danger" onclick={() => void planPreserveDataDecommission()} disabled={decommissionBusy}>{decommissionMessage('preservePlan')}</button></div>
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
                        <div class="actions"><button type="button" onclick={() => void resumePreserveDataDecommission()} disabled={decommissionBusy}>{decommissionMessage('resume')}</button></div>
                      {/if}
                    {/if}
                  {/if}
                  <hr />
                  <p class="muted">{decommissionMessage('forgetDescription')}</p>
                  <div class="actions"><button type="button" class="secondary" onclick={() => activeProfile && void forgetProfile(activeProfile)} disabled={decommissionBusy}>{decommissionMessage('forget')}</button></div>
                </section>

                <section class="decommission-card full-decommission-card">
                  <p class="eyebrow">{decommissionMessage('irreversible')}</p>
                  <h3 id="full-decommission-title">{decommissionMessage('fullTitle')}</h3>
                  <p class="muted">{decommissionMessage('fullDescription')}</p>
                  {#if fullDecommissionError}<p class="inline-error" role="alert">{fullDecommissionError}</p>{/if}
                  {#if !fullDecommissionPlan}
                    <div class="actions"><button type="button" class="danger" onclick={() => void planFullDecommission()} disabled={fullDecommissionBusy}>{decommissionMessage('fullPlan')}</button></div>
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
                    <label>{decommissionMessage('typedConfirmationPrompt')}
                      <code class="typed-confirmation">{fullDecommissionPlan.decommission.typedConfirmation}</code>
                      <input bind:value={fullDecommissionConfirmation} autocomplete="off" spellcheck="false" aria-label={decommissionMessage('typedConfirmation')} />
                    </label>
                    <div class="actions">
                      <button type="button" class="secondary" onclick={() => void planFullDecommission()} disabled={fullDecommissionBusy}>{decommissionMessage('discardReinspect')}</button>
                      <button type="button" class="danger" onclick={() => void approveFullDecommission()} disabled={fullDecommissionBusy || fullDecommissionConfirmation !== fullDecommissionPlan.decommission.typedConfirmation || (fullDecommissionPlan.decommission.requiresOwnerOverride && (!fullDecommissionOverride || !fullDecommissionOverrideReason.trim()))}>{decommissionMessage('typeConfirm')}</button>
                    </div>
                    {#if fullDecommissionRun}
                      <p class="inline-notice">{decommissionMessage('run')}: <code>{fullDecommissionRun.id}</code> — {runLabel(fullDecommissionRun.state)} {decommissionMessage('at')} {fullDecommissionRun.currentCheckpoint}</p>
                      <div class="actions">
                        {#if fullDecommissionRun.state === 'running' && fullDecommissionRun.currentCheckpoint.includes('interrupted')}
                          <button type="button" onclick={() => void resumeFullDecommission()} disabled={fullDecommissionBusy}>{decommissionMessage('resumeScope')}</button>
                        {/if}
                        {#if fullDecommissionRun.state === 'verified'}
                          <button type="button" class="secondary" onclick={exportFullDecommissionActivity}>{decommissionMessage('exportRecord')}</button>
                        {/if}
                      </div>
                    {/if}
                  {/if}
                </section>
              {/if}
            {/if}
          </section>
        </div>
        {/if}
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
  aside .aside-heading { margin: 1.75rem 0 .6rem; }
  nav { display: grid; gap: .5rem; margin-bottom: 1rem; }
  aside nav button { display: grid; text-align: left; gap: .2rem; color: #233c2e; background: transparent; border: 1px solid transparent; }
  aside nav button:hover, aside nav button.active { background: white; border-color: #b9cbbf; }
  aside nav button small { font-weight: 500; color: #46564c; }
  .profile-row { display: grid; grid-template-columns: 1fr auto; align-items: stretch; gap: .25rem; }
  .profile-pick { min-width: 0; }
  .profile-pick span { overflow-wrap: anywhere; }
  /* Quiet until wanted: removing an installation is never the reason someone
     opens the sidebar, so it stays out of the way without hiding entirely. */
  .profile-remove { align-self: center; padding: .35rem .55rem; color: #7a5350; }
  .profile-row:hover .profile-remove, .profile-remove:focus-visible { background: #fff1ee; color: #78281f; }
  .aside-status { display: flex; align-items: flex-start; gap: .6rem; padding: .7rem .85rem; border-radius: .8rem; background: white; border: 1px solid #cbd9cf; font-weight: 750; }
  .aside-status div { display: grid; gap: .15rem; min-width: 0; }
  .aside-status small { font-weight: 500; color: #5f6c64; overflow-wrap: anywhere; }
  .aside-status .status-dot { flex: 0 0 auto; margin-top: .42rem; width: .6rem; height: .6rem; border-radius: 50%; background: #8ca092; }
  .aside-status.verified { background: #daf1e1; border-color: #a9d3ba; color: #145f3d; }
  .aside-status.verified .status-dot { background: #176b45; }
  main { width: min(58rem, 100%); padding: clamp(1.5rem, 5vw, 4rem); }
  .centered { margin: 4rem auto; }
  h1 { margin: .2rem 0; font-size: clamp(2rem, 5vw, 3.5rem); letter-spacing: -.04em; }
  h2 { margin: .2rem 0 .7rem; }
  .eyebrow { margin: 0 0 .35rem; color: #5b6e61; font-size: .78rem; font-weight: 800; text-transform: uppercase; letter-spacing: .12em; }
  .profile-heading { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 1rem; }
  .card { border-radius: 1rem; background: white; border: 1px solid #d5ded7; box-shadow: 0 10px 30px rgba(26, 55, 38, .06); padding: clamp(1.25rem, 3vw, 2rem); margin: 0 0 2rem; }

  /* Three places, side by side, so the record of what happened and the
     installation itself are never something to scroll a stage past. */
  .tabs { margin: 0 0 1.5rem; }
  .tabs [role="tablist"] { display: flex; gap: .3rem; flex-wrap: wrap; border-bottom: 1px solid #cbd9cf; }
  .tabs [role="tab"] { background: transparent; color: #2c4a37; border: 1px solid transparent; border-bottom: 0; border-radius: .7rem .7rem 0 0; padding: .6rem 1rem; font-weight: 750; }
  .tabs [role="tab"]:hover { background: #eef3ef; }
  .tabs [role="tab"].selected { background: white; border-color: #cbd9cf; color: #123b2a; box-shadow: 0 -2px 0 inset #176b45; }

  /* The one thing worth doing next, said before anything else on the screen. */
  .next-action { border-left: 5px solid #ef9f27; background: white; border: 1px solid #d5ded7; border-left: 5px solid #ef9f27; border-radius: 1rem; padding: 1.1rem 1.25rem; margin: 0 0 1.5rem; }
  .next-action h2, .next-action h3 { margin: .1rem 0 .4rem; }

  /* Journey rail: where the operator is, what is behind them, what is still
     out of reach and why. Wraps to a single column on narrow screens. */
  .journey-rail { margin: 0 0 1.5rem; }
  .journey-rail ol { list-style: none; margin: 0; padding: 0; display: grid; gap: .4rem; grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr)); }
  .journey-rail li { display: flex; }
  .journey-rail button { width: 100%; display: grid; grid-template-columns: auto 1fr; gap: .15rem .6rem; align-items: start; text-align: left; background: white; border: 1px solid #d5ded7; border-radius: .75rem; padding: .7rem .8rem; color: inherit; font: inherit; min-height: 0; }
  .journey-rail li.locked button { background: #f4f7f5; }
  .journey-rail .rail-index { grid-row: 1 / span 2; display: grid; place-items: center; width: 1.6rem; height: 1.6rem; border-radius: 50%; background: #e6ede8; color: #2c4a37; font-weight: 800; font-size: .8rem; }
  .journey-rail .rail-title { font-weight: 750; font-size: .92rem; }
  .journey-rail .rail-state { font-size: .76rem; color: #5b6e61; }
  .journey-rail li.done button { border-color: #b7cfc0; }
  .journey-rail li.done .rail-index { background: #2c6b45; color: white; }
  .journey-rail li.current button { border-color: #2c6b45; box-shadow: 0 0 0 2px rgba(44, 107, 69, .18); }
  .journey-rail li.current .rail-index { background: #2c6b45; color: white; }
  .step-heading { margin-bottom: 1rem; }
  /* Forwards and backwards in the same place on every stage. */
  .step-navigation { display: flex; justify-content: space-between; gap: .7rem; margin: 0 0 2rem; }

  /* Answers that are right for almost everyone, kept out of the decision. */
  .advanced { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
  .advanced-body { display: grid; gap: 1rem; margin-top: 1rem; }
  .asset-detail { margin: .5rem 0 1rem; }
  .asset-detail summary { cursor: pointer; font-weight: 750; }

  .blocker-list { margin: 0; padding-left: 1.1rem; display: grid; gap: .35rem; }
  .blocker-list code { color: #6b7a70; }
  /* Destructive and unbacked, so it is spelled out rather than summarised. */
  .foreign-install { margin-top: 1.25rem; border: 1px solid #b5473b; border-radius: .75rem; padding: 1rem; }
  .foreign-install h3 { margin: 0 0 .5rem; }
  .foreign-install ul { margin: .4rem 0; padding-left: 1.1rem; display: grid; gap: .3rem; }
  .provider-choice { display: grid; gap: .5rem; border: 1px solid #d5ded7; border-radius: .75rem; padding: 1rem; margin-bottom: 1.25rem; }
  .provider-choice legend { font-weight: 750; padding: 0 .4rem; }
  .retire-card { border-color: #e3cfcf; }
  .form-card { max-width: 42rem; }
  form, form label { display: grid; gap: .5rem; }
  form { gap: 1.25rem; }
  form label span { font-weight: 750; }
  input, select, textarea { width: 100%; min-height: 2.8rem; border: 1px solid #9eb0a4; border-radius: .65rem; padding: .65rem .75rem; background: white; color: #17211b; }
  .form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
  .actions { display: flex; justify-content: flex-end; gap: .7rem; flex-wrap: wrap; }
  button.secondary { background: transparent; border: 1px solid #8ca092; color: #244932; }
  button.secondary:hover { background: #eef3ef; }
  button.full { width: 100%; }
  .badge { display: inline-flex; border-radius: 2rem; background: #e6f1e9; color: #176b45; padding: .25rem .6rem; font-size: .8rem; font-weight: 800; }
  .run-status { display: flex; align-items: center; gap: .65rem; min-height: 3rem; margin: 1rem 0 2rem; padding: .65rem 1rem; border-radius: .8rem; background: #e8ede9; font-weight: 800; }
  .run-status small { margin-left: auto; color: #68766d; font-weight: 600; }
  .run-status.verified { background: #daf1e1; color: #145f3d; }
  .status-icon { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border-radius: 50%; background: currentColor; color: white; }
  .verified .status-icon { background: #176b45; }
  .decommission-card { margin: 1.5rem 0 0; border-left: 5px solid #b5473b; padding-left: 1rem; }
  .decommission-card hr { border: 0; border-top: 1px solid #dce5de; margin: 1.5rem 0; }
  .vault-card { border-left: 5px solid #176b45; }
  .vault-heading { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .vault-heading h2 { margin-bottom: 0; }
  .badge.unlocked { background: #daf1e1; color: #145f3d; }
  .facility-state { display: flex; align-items: center; gap: .55rem; font-weight: 750; }
  .facility-state span { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border-radius: 50%; background: #e6eee8; }
  .fallback { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
  .fallback h3 { margin: 0; }
  .inline-error { padding: .8rem; border-radius: .65rem; background: #fff1ee; color: #78281f; }
  .credential-metadata { margin: 1.25rem 0 0; }
  .recovery-card { border-left: 5px solid #ef9f27; }
  .capability-card { border-left: 5px solid #315c9a; }
  .install-card { border-left: 5px solid #2c6b45; }
  .finish-card { border-left: 5px solid #ef9f27; }
  fieldset { display: grid; gap: .6rem; border: 1px solid #dce5de; border-radius: .65rem; padding: 1rem; }
  legend { font-weight: 750; }
  .check { display: flex; align-items: center; gap: .65rem; font-weight: 600; }
  .check input { width: 1.1rem; min-height: 1.1rem; }
  .capability-preview { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
  .overlay-diff { overflow: auto; max-height: 22rem; padding: 1rem; border-radius: .65rem; background: #14241b; color: #e9f4eb; white-space: pre; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .recovery-form { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
  .recovery-form h3, .recovery-preview h3 { margin: 0; }
  .recovery-preview { margin-top: 1.25rem; border-top: 1px solid #dce5de; padding-top: 1.25rem; }
  .inline-notice { padding: .8rem; border-radius: .65rem; background: #e6f1e9; color: #145f3d; }
  button.danger { background: transparent; border: 1px solid #b5473b; color: #78281f; }
  button.danger:hover { background: #fff1ee; }
  .handoff-checklist, .handoff-steps ul, .handoff-limitations ul, .hetzner-inventory, .hetzner-presets { list-style: none; padding: 0; display: grid; gap: .5rem; }
  .handoff-checklist li { display: flex; gap: .6rem; align-items: baseline; }
  dl { display: grid; gap: .8rem; }
  dl div { display: grid; grid-template-columns: minmax(8rem, 11rem) 1fr; gap: 1rem; border-top: 1px solid #e0e6e1; padding-top: .8rem; }
  dt { color: #617066; font-weight: 700; }
  dd { margin: 0; overflow-wrap: anywhere; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em; }
  .timeline { list-style: none; padding: 0; display: grid; gap: .65rem; }
  .timeline li { display: flex; align-items: center; gap: .7rem; }
  .timeline li > span:first-child { width: .7rem; height: .7rem; flex: 0 0 auto; border-radius: 50%; background: #176b45; }
  .timeline .timeline-label code { color: #5f6c64; margin-left: .4rem; }
  .timeline time { margin-left: auto; color: #5f6c64; font-size: .85rem; }
  .muted { color: #5f6c64; }
  .error { display: flex; gap: 1rem; padding: 1rem; margin-bottom: 1rem; border: 1px solid #b5473b; border-radius: .8rem; background: #fff1ee; color: #78281f; }
  @media (max-width: 760px) {
    .product-header { align-items: flex-start; }
    .brand small, .locale-control span { display: none; }
    .shell { grid-template-columns: 1fr; }
    aside { border-right: 0; border-bottom: 1px solid #cbd9cf; padding: 1rem; }
    aside nav { grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr)); }
    main { padding: 1.25rem; }
    .form-grid, dl div { grid-template-columns: 1fr; }
    .profile-heading { align-items: stretch; flex-direction: column; }
    .actions { justify-content: stretch; }
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
    aside nav button, aside nav button small, .muted, .eyebrow, .run-status small, .timeline time { color: inherit; }
    aside nav button:hover, aside nav button.active, .card, .aside-status, .next-action { background: #1b3024; border-color: #466b55; }
    .aside-status small { color: #b9cec1; }
    .profile-remove { color: #f3d9d4; }
    .profile-row:hover .profile-remove, .profile-remove:focus-visible { background: #4a2622; color: #ffe9e4; }
    .aside-status.verified { background: #204a32; color: #e2f8e8; border-color: #3f7a58; }
    .tabs [role="tab"] { color: #edf6ef; }
    .tabs [role="tab"]:hover { background: #284433; }
    .tabs [role="tab"].selected { background: #1b3024; border-color: #466b55; color: #edf6ef; }
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
