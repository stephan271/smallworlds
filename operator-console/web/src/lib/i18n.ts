export type Locale = 'en' | 'de';

const messages = {
  en: {
    product: 'SmallWorlds Operator Console',
    subtitle: 'Set up and understand your cluster from one private place.',
    profiles: 'Your installations',
    createAnother: 'Set up another installation',
    createTitle: 'Set up a new installation',
    profileName: 'Name this installation',
    language: 'Language',
    deploymentMode: 'Where it runs',
    localLan: 'A computer in my building, private',
    localPublic: 'A computer in my building, reachable from the internet',
    hetzner: 'A rented server (Hetzner)',
    createProfile: 'Create installation',
    editProfile: 'Change details',
    saveProfile: 'Save',
    cancel: 'Cancel',
    next: 'Next recommended action',
    continue: 'Continue',
    task: 'Check this console works',
    taskDescription: 'Runs a harmless rehearsal that changes nothing, so you can see the console plan, work, and report back before it touches anything real.',
    inspectPlan: 'See what will happen',
    planTitle: 'What will happen',
    effect: 'Record verified launcher evidence',
    noRisk: 'No cost, downtime, exposure, data, or lockout risk.',
    digest: 'Plan fingerprint (digest)',
    approve: 'Approve and start',
    activity: 'Activity',
    ready: 'Ready',
    running: 'Running',
    verified: 'Verified',
    cancelled: 'Cancelled',
    failed: 'Failed',
    retry: 'Try again',
    loading: 'Opening a secure session…',
    vaultTitle: 'Password safe (Launcher Vault)',
    vaultDescription: 'Passwords and access tokens are locked away on this computer, encrypted. Once saved, they are never shown again — not even to you.',
    vaultLocked: 'Locked',
    vaultUnlocked: 'Unlocked',
    osStoreAvailable: 'Device credential store available',
    osStoreUnavailable: 'Device credential store unavailable',
    unlockWithOSStore: "Unlock using this computer's login",
    passphraseFallback: 'Or use a passphrase',
    passphraseFallbackDescription: 'Use at least 12 characters when this device has no usable credential store. You will need the passphrase after every launcher restart.',
    vaultPassphrase: 'Safe passphrase',
    unlockVault: 'Open the safe',
    gitProviderToken: 'Access token for your settings repository',
    credentialExpiry: 'When this token stops working',
    storeCredential: 'Store credential',
    replaceCredential: 'Replace credential',
    removeCredential: 'Remove credential',
    noCredential: 'No credential stored',
		credentialPresent: 'Stored',
    credentialSource: 'Source',
    credentialExpires: 'Expires',
    rotationStatus: 'Rotation',
    sourceOperator: 'Operator',
    rotationCurrent: 'Current',
    rotationDueSoon: 'Due soon',
    rotationExpired: 'Expired',
    osCredentialStoreUnavailable: 'The device credential store is unavailable. Unlock with the passphrase fallback.',
    vaultPassphraseIncorrect: 'The passphrase is incorrect. Try again without sharing it.',
    vaultPassphraseTooShort: 'Use a passphrase with at least 12 characters.',
    vaultWrappingKeyMissing: 'The device credential store no longer contains this vault key. Use the recovery path before changing data.',
    vaultUnlockFailed: 'The Launcher Vault could not be unlocked. Check access to the launcher data directory and try again.',
    credentialStorageFailed: 'The credential could not be stored. The existing credential was not shown.',
    credentialRemovalFailed: 'The credential could not be removed. Try again.',
    recoveryEyebrow: 'Recovery and transfer',
    recoveryTitle: 'Recovery Bundle',
    recoveryDescription: 'Create an encrypted portable handoff, or safely preview and import one from another launcher.',
    recoveryExport: 'Create bundle',
    recoveryExportDescription: 'The encrypted bundle includes this profile, workflow history, credential material, and future cluster connection material.',
    recoveryPassphrase: 'Recovery passphrase',
    recoveryPassphraseHint: 'At least 12 characters',
    recoveryRecipients: 'Advanced age recipients',
    recoveryRecipientsHint: 'age1… (one public recipient per line)',
    recoveryRecipientChoice: 'Use a passphrase or one or more public age recipients, not both.',
    recoveryDownload: 'Download encrypted bundle',
    recoveryExported: 'Encrypted Recovery Bundle downloaded.',
    recoveryImport: 'Import bundle',
    recoveryImportDescription: 'Preview the source identity before explicitly transferring lifecycle authority.',
    recoveryBundleFile: 'Recovery Bundle file',
    recoveryUnlockMethod: 'Unlock method',
    recoveryAgeIdentity: 'Private age identity',
    recoveryPreview: 'Preview bundle',
    recoveryClusterId: 'Cluster ID',
    recoveryFormat: 'Bundle format',
    recoveryConfirmDescription: 'Confirm only if this is the cluster whose lifecycle authority should move to this Launcher Host.',
    recoveryConfirmImport: 'Confirm and import',
    recoveryImported: 'Lifecycle authority transferred to this Launcher Host.',
    recoveryCredentialsIncorrect: 'The Recovery Bundle could not be unlocked with those credentials.',
    recoveryAuthorityExists: 'This Launcher Host already has lifecycle authority for that cluster.',
    recoveryIdentityMismatch: 'The confirmed cluster identity does not match the bundle preview.',
    recoveryVaultLocked: 'Unlock the Launcher Vault before importing protected material.',
    recoveryFailed: 'The Recovery Bundle could not be processed safely.',
    localBootstrapEffectPrivileged: 'Runs the approved installer with root or sudo privileges.',
    localBootstrapEffectData: 'Creates and uses the selected persistent data path.',
    localBootstrapEffectK3S: 'Installs the pinned k3s release from the verified payload.',
    localBootstrapEffectArgoCD: 'Installs Argo CD and points its root application at the exact reviewed overlay commit.',
    localBootstrapRiskExposure: 'Opens cluster services on ports 80, 443, and 6443 on the node.',
    localBootstrapRiskDowntime: 'k3s and workloads can restart and remain unavailable until convergence completes.',
    localBootstrapRiskCancellation: 'Cancellation waits for the current atomic install operation to finish.',
    localBootstrapRiskRecovery: 'Retries preserve persistent data and resume only from durable checkpoints.',
    localPublicEffectDDNS: 'Runs an in-cluster DDNS task every five minutes to keep public records on the current IPv4 address.',
    localPublicEffectCertificates: 'Obtains publicly trusted certificates after DNS and router forwarding converge.',
    localPublicEffectMemberIngress: 'Publishes member-facing applications while operator interfaces remain absent from public ingress.',
    localPublicEffectHeadscale: 'Publishes Headscale coordination so operator devices can enroll remotely.',
    localPublicRiskRouter: 'The Launcher relies on your router acknowledgement and does not configure or separately probe forwarding.',
    localPublicRiskPropagation: 'DNS propagation and certificate issuance can take several minutes; the run waits and can be resumed.',
    localPublicRouterTitle: 'Manual router forwarding', localPublicRouterDescription: 'Forward these ports on your router to the Local Cluster Node. Keep SSH and Kubernetes (6443/tcp) private.', localPublicRouterHTTP: 'HTTP certificate challenge and HTTPS redirect', localPublicRouterHTTPS: 'public member applications and public Headscale coordination', localPublicRouterJitsi: 'Jitsi media traffic', localPublicRouterNoAutomation: 'The Launcher does not use UPnP, NAT-PMP, router vendor APIs, or a dedicated forwarding probe.', localPublicDNSProvider: 'DNS and DNS-01 provider', localPublicDNSToken: 'Hetzner DNS token (write-only)', localPublicDDNS: 'The in-cluster DDNS task checks the public IPv4 every five minutes. DNS propagation and certificate issuance remain a resumable waiting step.', localPublicRouterAcknowledge: 'I configured all three forwarding rules on the router.', localPublicMailWarning: 'Mail delivery from residential internet is unreliable: providers may block port 25, and home addresses commonly lack PTR records or have poor reputation. Use an SMTP relay.', localPublicJitsiWarning: 'Jitsi media requires the 10000/udp forward; calls may connect without usable audio or video when it is missing.', localPublicHandoffTitle: 'Hand over administration (internet-facing setup)', localPublicHandoffDescription: 'Connect this computer and check the private entrance before removing temporary access. Member apps and the network coordination are reachable from the internet; the operator console, Grafana, and Argo CD stay private.'
    ,capabilityEyebrow: 'Cluster design', capabilityTitle: 'What your community gets', capabilityDescription: 'The parts that keep everything running are always installed. Choose the apps your members will use, then read the exact change before anything is proposed. No passwords appear in it.', capabilityMode: 'How much to install', capabilityMinimal: 'Minimal', capabilityCollaboration: 'Collaboration', capabilityFull: 'Full', capabilityCustom: 'Custom', capabilityRelease: 'SmallWorlds version', capabilityRepository: 'Your private settings repository', capabilityDomain: 'Your web address', capabilityCommunityApps: 'Community applications', capabilityReview: 'Show me the exact changes', capabilityPreview: 'Before anything happens', capabilityPlanReady: 'Deterministic overlay preview', capabilityMemory: 'Estimated memory', capabilityStorage: 'Estimated storage', capabilityExposure: 'Exposure', capabilityProtection: 'Protection expectations', capabilityOverlayDiff: 'The exact changes', bootstrapAssetEyebrow: 'Bootstrap prerequisites', bootstrapAssetTitle: 'Installer files', bootstrapAssetDescription: 'See exactly which files this version needs and where they come from, before any of them are downloaded. Each one is checked against its signature.', offlineBundleFuture: 'Offline Bundle support is planned future work; initial bootstrap requires internet access.', bootstrapAssetInspect: 'Show which files are needed', bootstrapAssetAcquire: 'Download and check the files', bootstrapAssetUnavailable: 'No compatible signed bootstrap asset manifest is published for this release yet.', nodeEyebrow: 'Cluster node', nodeTitle: 'The computer that will run it', nodeDescription: 'Point this console at the Linux machine that will run your cluster. Nothing is changed — it is only looked at, and only after you confirm you recognise it.', nodeTarget: 'Target', nodeRemote: 'Remote Linux node', nodeSameHost: 'This Linux Launcher Host', nodeHost: 'Its name or address on the network', nodePort: 'SSH port', nodeUsername: 'SSH username', nodeAuthentication: 'SSH authentication', nodeAgent: 'SSH agent', nodePrivateKey: 'Private key', nodePassword: 'Password', nodeKeyPassphrase: 'Private-key passphrase', nodeSudoPassword: 'Sudo password (if required)', nodeProbe: 'Show SSH fingerprint', nodeFingerprint: 'SSH host-key fingerprint', nodeTrust: 'Confirm and trust', nodeInspect: 'Check this computer', nodeOperatingSystem: 'Operating system', nodeCapacity: 'Available capacity', nodeAssessment: 'Assessment', nodeReady: 'Suitable — ready to continue', nodePlanSSHKey: 'Plan dedicated SSH key', githubEyebrow: 'Git provider', githubTitle: 'GitHub overlay access', githubDescription: 'Create a fine-grained personal access token. The token is validated and retained only in the encrypted Launcher Vault.', githubTokenGuide: 'Open GitHub token settings', githubAuthority: 'Authority purpose', githubCreationAuthority: 'Temporary repository creation', githubOngoingAuthority: 'Repository-scoped ongoing access', githubToken: 'Fine-grained GitHub token', githubValidate: 'Validate and store token', githubOwner: 'GitHub owner', githubNoExpiry: 'No expiry reported', githubRepositoryName: 'New private repository name', githubEstablish: 'Establish GitHub overlay', genericGitEyebrow: 'Existing Git repository', genericGitTitle: 'HTTPS Git overlay access', genericGitDescription: 'Use an empty HTTPS repository. Its username and token are validated, encrypted in the Launcher Vault, and never returned to the browser.', genericGitUsername: 'Git username', genericGitToken: 'Git access token', genericGitValidate: 'Validate and store access', genericGitApprovalHint: 'Approve the exact change plan below before initializing this empty repository.', genericGitEstablish: 'Initialize HTTPS Git overlay', genericGitPropose: 'Push reviewed change branch', genericGitManualMerge: 'Review this branch in your Git provider and merge it manually:', localBootstrapEyebrow: 'Cluster installation', localBootstrapTitle: 'Install onto this computer', localBootstrapDescription: 'Reinspect this node and review the privileged, resumable installation before approving it.', localBootstrapEnvironment: 'Environment extension', localBootstrapDataDirectory: "Where your community's data is kept", localBootstrapNodeName: 'Kubernetes node name', localBootstrapACMEEmail: 'Certificate email (optional)', localBootstrapManageDNS: 'Manage public DNS records', localBootstrapSecrets: 'Kubernetes Secret manifests (kept outside Git)', localBootstrapReview: 'Reinspect and create Change Plan', localBootstrapOverlayCommit: 'GitOps overlay commit', handoffEyebrow: 'Handing over administration', handoffTitle: 'Hand administration to the cluster', handoffDescription: 'Set up trusted HTTPS and private-only access, connect this computer, then hand day-to-day administration to the first owner. Nothing is put on the internet.', handoffUnlockFirst: 'Open the password safe first — these steps need what is in it.', handoffStepsTitle: 'Progress', handoffStepClusterCA: "This device trusts your cluster's certificates", handoffStepPrivateNetwork: 'Private network and its addresses set up', handoffStepLauncherEnrolled: 'This computer joined the private network', handoffStepGatewayIdentity: 'The private entrance has its own identity', handoffStepGatewayAccess: 'Only encrypted connections are accepted', handoffStepVerified: 'Checked: reachable privately, addresses resolve, certificates valid', handoffStepClosed: 'Temporary direct access removed', handoffStepFirstOwner: 'First owner registered', handoffClusterCAEstablish: "Create your cluster's certificate authority", handoffDeviceTrustInstall: 'Trust it on this device', handoffDeviceTrustFingerprint: 'Certificate fingerprint', handoffBaseDomain: 'Private web address', handoffPrivateNetworkEstablish: 'Set up the private network', handoffTailscaleDetect: 'Look for the Tailscale app', handoffTailscaleDetected: 'The official Tailscale app is installed on this computer.', handoffTailscaleAbsent: 'No Tailscale app found on this computer.', handoffTailscaleAcquire: 'A checked download is available. Installing it needs administrator rights.', handoffTailscaleManual: 'Install the official app yourself', handoffEnrollmentEstablish: 'Create the credentials for joining', handoffLauncherConsume: 'Use the one-time credential to join', handoffVerify: 'Check that private access works', handoffCloseAccess: 'Remove temporary direct access', handoffFirstOwnerClaim: 'Start registering the first owner', handoffFirstOwnerRegister: 'Register a passkey and close the setup door for good', handoffLimitations: 'What private-only means', handoffConsoleUrl: "Your cluster's own console"
    ,offsiteEyebrow: 'Disaster protection', offsiteTitle: 'Backup copy somewhere else', offsiteDescription: 'A backup on the same machine is lost with the machine. Point this at storage somewhere else so a fire, a theft, or a dead disk is not the end of your community. The access keys stay in the safe; only the address is written down.', offsiteEndpoint: 'S3 endpoint (HTTPS)', offsiteRegion: 'Region', offsiteBucket: 'Bucket', offsiteAccessKey: 'Access key ID', offsiteSecretKey: 'Secret access key', offsiteInspect: 'Inspect destination', offsiteReachable: 'Reachable', offsiteVersioning: 'Object versioning', offsiteFingerprint: 'Access key fingerprint', offsiteVersioningEnabled: 'Enabled', offsiteVersioningDisabled: 'Disabled', offsiteVersioningUnsupported: 'Unsupported', offsiteVersioningUnknown: 'Not inspectable', offsiteAcknowledge: 'I acknowledge object versioning could not be confirmed; point-in-time recovery is not guaranteed.', offsitePlanReview: 'Review change plan', offsiteGitDiff: 'Non-secret Git change (proposed)', offsiteSecretEffect: 'Cluster Secret (values never leave the vault)', offsiteSecretKeysLabel: 'Keys', offsiteImplications: 'What this changes', offsiteImplData: 'A copy of all buckets is created offsite.', offsiteImplCost: 'Offsite storage and egress are billed by the destination.', offsiteImplProtection: 'Enables offsite disaster protection.', offsiteApprovePropose: 'Approve and open Git proposal', offsiteProposalOpened: 'Proposal opened — review and merge it in your Git provider:', offsiteProposalRequired: 'Open and merge the Git proposal before validating.', offsiteValidate: 'Run bounded validation', offsiteValidationVerdict: 'Validation verdict', offsiteRemediation: 'Recommended next step', offsiteRecoveryPoint: 'Offsite recovery point', offsiteResultVerified: 'Offsite protection verified', offsiteResultLocalBackupFailed: 'Local backup failed', offsiteResultReplicationFailed: 'Replication to the destination failed', offsiteResultNoEvidence: 'No offsite recovery point observed', offsiteResultStale: 'Offsite recovery point is stale', offsiteResultVersioningUnsupported: 'Replicated, but versioning is not supported', offsiteResultPending: 'Validation pending', offsiteRemediationNone: 'Offsite protection is healthy; no action needed.', offsiteRemediationLocalBackupFailed: 'Fix the local backup job before retrying replication.', offsiteRemediationReplicationFailed: 'Check destination credentials and connectivity, then re-run validation.', offsiteRemediationNoEvidence: 'Confirm the replicator ran and wrote to the destination bucket.', offsiteRemediationStale: 'The last offsite copy is old; re-run replication and validate again.', offsiteRemediationVersioningUnsupported: 'Enable object versioning on the destination for point-in-time recovery.', offsiteRemediationPending: 'Validation has not produced a verdict yet.'
    ,hetznerEyebrow: 'Hetzner infrastructure', hetznerTitle: 'Inspect and plan Hetzner infrastructure', hetznerDescription: 'Validate the project token, inspect what already exists, check DNS delegation, and build a cost-bearing plan. Nothing in your Hetzner project is changed until you approve the plan.', hetznerToken: 'Hetzner project token (read and write)', hetznerTokenValidate: 'Validate and store token', hetznerTokenGuide: 'Open Hetzner project security settings', hetznerTokenFingerprint: 'Token fingerprint', hetznerProject: 'Hetzner project', hetznerTokenValid: 'Validated for reading and provisioning.', hetznerTokenMalformed: 'This is not a Hetzner project token. Nothing was sent to the provider.', hetznerTokenUnauthorized: 'Hetzner rejected this token. Create a new one in the project.', hetznerTokenReadOnly: 'This token can read but not provision. Create a Read & Write token.', hetznerTokenInconclusive: 'Hetzner could not answer right now — the token is neither accepted nor rejected. Try again shortly.', hetznerTokenProjectMismatch: 'This token belongs to a different Hetzner project than this profile. Provisioning was not re-pointed.', hetznerDomain: 'Base domain', hetznerEnvExt: 'Environment extension (optional)', hetznerInspect: 'Inspect project', hetznerInventory: 'What exists in the project', hetznerInspectedAt: 'Inspected', hetznerAdoptSelected: 'Adopt this resource', hetznerOwnershipShared: 'Shared across profiles', hetznerOwnershipProfileOwned: 'Owned by this profile', hetznerOwnershipAdoptable: 'Exists — adoption decision required', hetznerOwnershipConflicting: 'Owned by another profile', hetznerOwnershipUnknown: 'Similarly named — never adopted automatically', hetznerOwnershipAbsent: 'Will be created', hetznerSimilarNames: 'Similarly named resources found', hetznerDelegation: 'Nameserver delegation', hetznerDelegationConfirmed: 'The domain is delegated to Hetzner.', hetznerDelegationPartial: 'Only some nameservers point to Hetzner; DNS and certificates will be unreliable.', hetznerDelegationMissing: 'The domain is delegated elsewhere. Point it at Hetzner at your registrar.', hetznerDelegationUnknown: 'The delegation could not be checked yet, so a public installation stays blocked.', hetznerDelegationNotRequired: 'Not required for a LAN-only installation.', hetznerExpectedNameservers: 'Expected nameservers', hetznerObservedNameservers: 'Currently published nameservers', hetznerCapacity: 'Capacity and cost', hetznerPresetSmall: 'Small', hetznerPresetRecommended: 'Recommended', hetznerPresetHigh: 'High capacity', hetznerPresetAdvanced: 'Advanced', hetznerRequirement: 'Needed by your selected capabilities', hetznerLocation: 'Location', hetznerServerType: 'Server type', hetznerVolume: 'Data volume', hetznerMonthlyCost: 'Estimated monthly cost', hetznerPricesObservedAt: 'Prices read from Hetzner at', hetznerPresetUnavailable: 'Not available in this location right now.', hetznerPresetTooSmall: 'Too small for the capabilities you selected.', hetznerAdvancedHint: 'Advanced: choose the location, server type, and volume size yourself.', hetznerCostNoteVolumeGrows: 'A volume can be grown later but never shrunk.', hetznerCostNoteVolumeBillable: 'The volume stays billable until you delete it, even without a server.', hetznerCostNotePrimaryIP: 'A reserved Primary IP stays billable while it exists.', hetznerCostNoteSnapshots: 'Snapshots and backups are billed separately.', hetznerCostNoteVAT: 'Prices exclude VAT and any traffic overage.', hetznerCostNoteObserved: 'Estimated from the provider catalog read during planning.', hetznerToolchainTitle: 'Infrastructure toolchain', hetznerToolchainDescription: 'The pinned OpenTofu and Hetzner provider are downloaded and verified into private launcher storage. No globally installed tools are used.', hetznerToolchainAcquire: 'Download and verify toolchain', hetznerToolchainReady: 'Verified', hetznerToolchainPending: 'Not downloaded yet', hetznerToolchainUnavailable: 'No verified toolchain is published for this platform yet. The launcher will not fall back to a tool found on this computer.', hetznerWorkspace: 'State workspace', hetznerWorkspaceIsolated: 'Isolated for this profile', hetznerWorkspaceLocked: 'Locked by', hetznerWorkspaceBackups: 'Kept state backups', hetznerPlanBuild: 'Create Change Plan', hetznerPlanTitle: 'Infrastructure Change Plan', hetznerPlanItems: 'Planned resources', hetznerPlanBlockers: 'Resolve before approving', hetznerPlanApprovable: 'Ready to approve. Approving is the first step that can change your Hetzner project.', hetznerApprove: 'Approve plan', hetznerActionCreate: 'Create', hetznerActionAdopt: 'Adopt', hetznerActionReuseShared: 'Reuse (shared)', hetznerActionKeep: 'Keep', hetznerActionBlocked: 'Blocked', hetznerBlockerAdoption: 'An existing resource must be explicitly adopted or renamed.', hetznerBlockerConflict: 'A resource of this name belongs to another Cluster Profile.', hetznerBlockerSimilar: 'A similarly named resource exists; resolve it in the Hetzner project first.', hetznerBlockerDelegation: 'Delegate the domain to Hetzner before a public installation.', hetznerBlockerUnavailable: 'The chosen server type cannot be created in this location right now.', hetznerBlockerCapacity: 'The chosen capacity is below what your selected capabilities need.', hetznerBlockerIncomplete: 'The inspection did not complete; inspect again before planning.', hetznerPlanStale: 'The project changed since this plan was made. Inspect and plan again.', hetznerBlockerSharedPrerequisite: 'The DNS zone and the shared admin SSH key belong to the whole project. Create them in Hetzner first; this installation reuses them and never owns them.', hetznerAcmeEmail: 'Certificate account address', hetznerAcmeEmailHint: "Let's Encrypt uses this address for expiry warnings about your community's certificates. It is stored as an ordinary contact detail, not as a credential.", hetznerAccessTitle: 'Temporary administration access', hetznerAccessDescription: 'While the cluster is being built, SSH and the Kubernetes API are reachable from the internet so you can watch it come up. This access is removed once private administration is verified — never before, because it is your only way back in if something goes wrong.', hetznerAccessState: 'Access', hetznerAccessOpen: 'Open', hetznerAccessClosed: 'Removed', hetznerAccessScope: 'Reachable from', hetznerAccessUnscoped: 'Anywhere on the internet', hetznerAccessReason: 'Why', hetznerAccessAddress: 'Your public address', hetznerAccessAddressHint: 'Narrowing access to your own address is safer, but only if that address is stable. If it changes — on a mobile connection, or a home connection that renumbers — you would lock yourself out.', hetznerAccessNarrow: 'Restrict to this address', hetznerAccessReasonScoped: 'Restricted to your address alone.', hetznerAccessReasonUnobserved: 'Your public address is not known yet, so access stays open.', hetznerAccessReasonNotRoutable: 'That is a local address, not the one Hetzner sees. Restricting to it would lock everyone out, so access stays open.', hetznerAccessReasonShared: 'That address is shared with other customers of your provider and changes without warning. Restricting to it would be both weaker than it looks and risky, so access stays open.'
    // --- Guided journey ----------------------------------------------------
    // Plain wording first, with the technical term in brackets where an
    // operator may need it to search the documentation or ask for help.
    ,journeyProgress: 'Setup progress'
    ,stepDone: 'Done'
    ,stepCurrent: 'You are here'
    ,stepLocked: 'Not yet'
    ,stepChange: 'Change'
    ,stepOf: 'Step {n} of {total}'
    ,stepCapabilitiesTitle: 'Choose what your community gets'
    ,stepCapabilitiesSummary: 'Pick the apps your members will use, and the web address they will visit.'
    ,stepAssetsTitle: 'Download the installer files'
    ,stepAssetsSummary: 'Fetch and check the exact files this version needs. Nothing is installed yet.'
    ,stepNodeTitle: 'Choose the computer that will run it'
    ,stepNodeSummary: 'Point the console at the machine, check it is suitable, then install onto it.'
    ,stepHetznerTitle: 'Rent and prepare the server'
    ,stepHetznerSummary: 'Inspect your hosting account, pick a size, and review the cost before anything is created.'
    ,stepSettingsRepoTitle: 'Choose where your settings are kept'
    ,stepSettingsRepoSummary: 'Your cluster reads its settings from a private Git repository. Create a new one, or use one you already have.'
    ,stepHandoffTitle: 'Hand administration to the cluster'
    ,stepHandoffSummary: 'Move day-to-day administration off this computer and onto the cluster itself.'
    ,stepProtectTitle: 'Protect against losing the machine'
    ,stepProtectSummary: 'Copy backups to storage somewhere else, so a dead machine is not a lost community.'
    ,stepBlockedChooseFirst: 'First choose what your community gets.'
    ,stepBlockedInstallersFirst: 'First download the installer files.'
    ,stepBlockedMachineFirst: 'First get the computer ready.'
    ,stepBlockedRepositoryFirst: 'First choose where your settings are kept.'
    ,settingsRepoChoice: 'Where should your settings live?'
    ,settingsRepoGitHub: 'Create a new private repository on GitHub'
    ,settingsRepoGeneric: 'Use a Git repository I already have'
    ,retireTitle: 'Shut this installation down'
    ,retireDescription: 'Only needed when you are taking the cluster out of service. Not part of setting one up.'
    ,retireShow: 'Show shutdown options'
    ,retireHide: 'Hide shutdown options'

    ,secretAlreadySaved: 'Already saved. Leave blank to keep it, or type a new one to replace it.'

    ,foreignInstallFound: 'Something else is already installed on this computer and is in the way. SmallWorlds will not install over it without your say-so.'
    ,foreignInstallRemove: 'Remove what is in the way'
    ,foreignInstallRemoving: 'Removing…'
    ,nodeSSHKeyPlanned: 'Key planned'

  },
  de: {
    product: 'SmallWorlds Operator Console',
    subtitle: 'Richten Sie Ihren Cluster an einem privaten Ort ein und behalten Sie ihn im Blick.',
    profiles: 'Ihre Installationen',
    createAnother: 'Weitere Installation einrichten',
    createTitle: 'Neue Installation einrichten',
    profileName: 'Name dieser Installation',
    language: 'Sprache',
    deploymentMode: 'Wo es läuft',
    localLan: 'Ein Rechner bei mir, nur intern',
    localPublic: 'Ein Rechner bei mir, aus dem Internet erreichbar',
    hetzner: 'Ein gemieteter Server (Hetzner)',
    createProfile: 'Installation anlegen',
    editProfile: 'Angaben ändern',
    saveProfile: 'Speichern',
    cancel: 'Abbrechen',
    next: 'Nächste empfohlene Aktion',
    continue: 'Weiter',
    task: 'Prüfen, ob diese Konsole funktioniert',
    taskDescription: 'Führt eine harmlose Probe aus, die nichts verändert. So sehen Sie, wie die Konsole plant, arbeitet und berichtet, bevor sie etwas Echtes anfasst.',
    inspectPlan: 'Zeigen, was passieren wird',
    planTitle: 'Was passieren wird',
    effect: 'Geprüften Launcher-Nachweis festhalten',
    noRisk: 'Keine Kosten-, Ausfall-, Freigabe-, Daten- oder Aussperrungsrisiken.',
    digest: 'Plan-Fingerabdruck (Digest)',
    approve: 'Genehmigen und starten',
    activity: 'Aktivität',
    ready: 'Bereit',
    running: 'Wird ausgeführt',
    verified: 'Verifiziert',
    cancelled: 'Abgebrochen',
    failed: 'Fehlgeschlagen',
    retry: 'Erneut versuchen',
    loading: 'Sichere Sitzung wird geöffnet …',
    vaultTitle: 'Passwort-Tresor (Launcher-Tresor)',
    vaultDescription: 'Passwörter und Zugriffstoken werden verschlüsselt auf diesem Computer verwahrt. Einmal gespeichert, werden sie nie wieder angezeigt — auch Ihnen nicht.',
    vaultLocked: 'Gesperrt',
    vaultUnlocked: 'Entsperrt',
    osStoreAvailable: 'Zugangsspeicher des Geräts verfügbar',
    osStoreUnavailable: 'Zugangsspeicher des Geräts nicht verfügbar',
    unlockWithOSStore: 'Mit der Anmeldung dieses Computers entsperren',
    passphraseFallback: 'Oder eine Passphrase verwenden',
    passphraseFallbackDescription: 'Verwenden Sie mindestens 12 Zeichen, wenn dieses Gerät keinen nutzbaren Zugangsspeicher hat. Nach jedem Launcher-Neustart wird die Passphrase erneut benötigt.',
    vaultPassphrase: 'Tresor-Passphrase',
    unlockVault: 'Tresor öffnen',
    gitProviderToken: 'Zugriffstoken für Ihr Einstellungs-Repository',
    credentialExpiry: 'Wann dieses Token abläuft',
    storeCredential: 'Zugangsschlüssel speichern',
    replaceCredential: 'Zugangsschlüssel ersetzen',
    removeCredential: 'Zugangsschlüssel entfernen',
    noCredential: 'Kein Zugangsschlüssel gespeichert',
		credentialPresent: 'Gespeichert',
    credentialSource: 'Quelle',
    credentialExpires: 'Läuft ab',
    rotationStatus: 'Rotation',
    sourceOperator: 'Operator',
    rotationCurrent: 'Aktuell',
    rotationDueSoon: 'Bald fällig',
    rotationExpired: 'Abgelaufen',
    osCredentialStoreUnavailable: 'Der Zugangsspeicher des Geräts ist nicht verfügbar. Entsperren Sie mit der Passphrasen-Alternative.',
    vaultPassphraseIncorrect: 'Die Passphrase ist falsch. Versuchen Sie es erneut, ohne sie weiterzugeben.',
    vaultPassphraseTooShort: 'Verwenden Sie eine Passphrase mit mindestens 12 Zeichen.',
    vaultWrappingKeyMissing: 'Der Zugangsspeicher des Geräts enthält diesen Tresorschlüssel nicht mehr. Verwenden Sie den Wiederherstellungsweg, bevor Sie Daten ändern.',
    vaultUnlockFailed: 'Der Launcher-Tresor konnte nicht entsperrt werden. Prüfen Sie den Zugriff auf das Launcher-Datenverzeichnis und versuchen Sie es erneut.',
    credentialStorageFailed: 'Der Zugangsschlüssel konnte nicht gespeichert werden. Der bestehende Wert wurde nicht angezeigt.',
    credentialRemovalFailed: 'Der Zugangsschlüssel konnte nicht entfernt werden. Versuchen Sie es erneut.',
    recoveryEyebrow: 'Wiederherstellung und Übertragung',
    recoveryTitle: 'Wiederherstellungspaket',
    recoveryDescription: 'Erstellen Sie eine verschlüsselte portable Übergabe oder prüfen und importieren Sie eine von einem anderen Launcher sicher.',
    recoveryExport: 'Paket erstellen',
    recoveryExportDescription: 'Das verschlüsselte Paket enthält dieses Profil, den Workflow-Verlauf, Zugangsmaterial und künftige Cluster-Verbindungsdaten.',
    recoveryPassphrase: 'Wiederherstellungs-Passphrase',
    recoveryPassphraseHint: 'Mindestens 12 Zeichen',
    recoveryRecipients: 'Erweiterte age-Empfänger',
    recoveryRecipientsHint: 'age1… (ein öffentlicher Empfänger pro Zeile)',
    recoveryRecipientChoice: 'Verwenden Sie eine Passphrase oder einen oder mehrere öffentliche age-Empfänger, nicht beides.',
    recoveryDownload: 'Verschlüsseltes Paket herunterladen',
    recoveryExported: 'Verschlüsseltes Wiederherstellungspaket heruntergeladen.',
    recoveryImport: 'Paket importieren',
    recoveryImportDescription: 'Prüfen Sie die Quellidentität, bevor Sie die Lifecycle-Autorität ausdrücklich übertragen.',
    recoveryBundleFile: 'Wiederherstellungspaket-Datei',
    recoveryUnlockMethod: 'Entsperrmethode',
    recoveryAgeIdentity: 'Private age-Identität',
    recoveryPreview: 'Paket prüfen',
    recoveryClusterId: 'Cluster-ID',
    recoveryFormat: 'Paketformat',
    recoveryConfirmDescription: 'Bestätigen Sie nur, wenn die Lifecycle-Autorität dieses Clusters auf diesen Launcher-Host wechseln soll.',
    recoveryConfirmImport: 'Bestätigen und importieren',
    recoveryImported: 'Die Lifecycle-Autorität wurde auf diesen Launcher-Host übertragen.',
    recoveryCredentialsIncorrect: 'Das Wiederherstellungspaket konnte mit diesen Zugangsdaten nicht entsperrt werden.',
    recoveryAuthorityExists: 'Dieser Launcher-Host hat bereits die Lifecycle-Autorität für diesen Cluster.',
    recoveryIdentityMismatch: 'Die bestätigte Cluster-ID stimmt nicht mit der Paketvorschau überein.',
    recoveryVaultLocked: 'Entsperren Sie den Launcher-Tresor, bevor Sie geschütztes Material importieren.',
    recoveryFailed: 'Das Wiederherstellungspaket konnte nicht sicher verarbeitet werden.',
    localBootstrapEffectPrivileged: 'Führt das genehmigte Installationsprogramm mit Root- oder Sudo-Rechten aus.',
    localBootstrapEffectData: 'Erstellt und verwendet den ausgewählten dauerhaften Datenpfad.',
    localBootstrapEffectK3S: 'Installiert die fixierte k3s-Version aus dem verifizierten Paket.',
    localBootstrapEffectArgoCD: 'Installiert Argo CD und bindet seine Root-Anwendung an den exakt geprüften Overlay-Commit.',
    localBootstrapRiskExposure: 'Öffnet Clusterdienste auf den Ports 80, 443 und 6443 des Knotens.',
    localBootstrapRiskDowntime: 'k3s und Workloads können neu starten und bis zum Abschluss der Konvergenz nicht verfügbar sein.',
    localBootstrapRiskCancellation: 'Ein Abbruch wartet, bis der laufende atomare Installationsschritt abgeschlossen ist.',
    localBootstrapRiskRecovery: 'Wiederholungen erhalten dauerhafte Daten und werden nur an beständigen Kontrollpunkten fortgesetzt.',
    nodePlanSSHKey: 'Dedizierten SSH-Schlüssel planen',
    localPublicEffectDDNS: 'Führt alle fünf Minuten eine clusterinterne DDNS-Aufgabe aus, damit öffentliche Einträge auf die aktuelle IPv4-Adresse zeigen.',
    localPublicEffectCertificates: 'Bezieht öffentlich vertrauenswürdige Zertifikate, sobald DNS und Router-Portweiterleitung funktionieren.',
    localPublicEffectMemberIngress: 'Veröffentlicht Mitglieder-Anwendungen; Operator-Oberflächen bleiben vom öffentlichen Ingress ausgeschlossen.',
    localPublicEffectHeadscale: 'Veröffentlicht die Headscale-Koordination, damit Operator-Geräte aus der Ferne angemeldet werden können.',
    localPublicRiskRouter: 'Der Launcher verlässt sich auf Ihre Router-Bestätigung und konfiguriert oder prüft die Weiterleitung nicht gesondert.',
    localPublicRiskPropagation: 'DNS-Ausbreitung und Zertifikatsausstellung können mehrere Minuten dauern; der Lauf wartet fortsetzbar.',
    localPublicRouterTitle: 'Manuelle Router-Portweiterleitung', localPublicRouterDescription: 'Leiten Sie diese Ports im Router an den lokalen Cluster-Knoten weiter. SSH und Kubernetes (6443/tcp) bleiben privat.', localPublicRouterHTTP: 'HTTP-Zertifikatsprüfung und HTTPS-Weiterleitung', localPublicRouterHTTPS: 'öffentliche Mitglieder-Anwendungen und öffentliche Headscale-Koordination', localPublicRouterJitsi: 'Jitsi-Medienverkehr', localPublicRouterNoAutomation: 'Der Launcher verwendet weder UPnP, NAT-PMP noch Router-Hersteller-APIs oder eine eigene Portweiterleitungsprüfung.', localPublicDNSProvider: 'DNS- und DNS-01-Anbieter', localPublicDNSToken: 'Hetzner-DNS-Token (nur schreibbar)', localPublicDDNS: 'Die clusterinterne DDNS-Aufgabe prüft die öffentliche IPv4-Adresse alle fünf Minuten. DNS-Ausbreitung und Zertifikatsausstellung bleiben ein fortsetzbarer Warteschritt.', localPublicRouterAcknowledge: 'Ich habe alle drei Portweiterleitungen im Router eingerichtet.', localPublicMailWarning: 'Mailversand über private Internetanschlüsse ist unzuverlässig: Anbieter können Port 25 sperren; Heimanschlüsse haben meist keinen PTR-Eintrag und eine schlechte Reputation. Verwenden Sie ein SMTP-Relay.', localPublicJitsiWarning: 'Jitsi-Medien benötigen die Weiterleitung von 10000/udp; ohne sie können Anrufe ohne nutzbares Audio oder Video verbunden werden.', localPublicHandoffTitle: 'Verwaltung übergeben (aus dem Internet erreichbar)', localPublicHandoffDescription: 'Verbinden Sie diesen Computer und prüfen Sie den privaten Zugang, bevor der vorübergehende Zugang entfernt wird. Mitglieder-Anwendungen und die Netzwerkkoordination sind aus dem Internet erreichbar; Operator Console, Grafana und Argo CD bleiben privat.'
    ,capabilityEyebrow: 'Cluster-Entwurf', capabilityTitle: 'Was Ihre Gemeinschaft bekommt', capabilityDescription: 'Die Bestandteile, die alles am Laufen halten, werden immer installiert. Wählen Sie die Anwendungen für Ihre Mitglieder und lesen Sie die genaue Änderung, bevor etwas vorgeschlagen wird. Passwörter erscheinen darin nicht.', capabilityMode: 'Wie viel installiert wird', capabilityMinimal: 'Minimal', capabilityCollaboration: 'Zusammenarbeit', capabilityFull: 'Vollständig', capabilityCustom: 'Benutzerdefiniert', capabilityRelease: 'SmallWorlds-Version', capabilityRepository: 'Ihr privates Einstellungs-Repository', capabilityDomain: 'Ihre Webadresse', capabilityCommunityApps: 'Community-Anwendungen', capabilityReview: 'Genaue Änderungen anzeigen', capabilityPreview: 'Bevor etwas passiert', capabilityPlanReady: 'Deterministische Overlay-Vorschau', capabilityMemory: 'Geschätzter Arbeitsspeicher', capabilityStorage: 'Geschätzter Speicher', capabilityExposure: 'Freigabe', capabilityProtection: 'Schutzerwartungen', capabilityOverlayDiff: 'Die genauen Änderungen', bootstrapAssetEyebrow: 'Bootstrap-Voraussetzungen', bootstrapAssetTitle: 'Installationsdateien', bootstrapAssetDescription: 'Sehen Sie genau, welche Dateien diese Version benötigt und woher sie kommen, bevor eine davon geladen wird. Jede wird gegen ihre Signatur geprüft.', offlineBundleFuture: 'Offline-Bundle-Unterstützung ist geplante Zukunftsarbeit; der erste Bootstrap benötigt Internetzugang.', bootstrapAssetInspect: 'Benötigte Dateien anzeigen', bootstrapAssetAcquire: 'Dateien herunterladen und prüfen', bootstrapAssetUnavailable: 'Für diese Version ist noch kein kompatibles signiertes Bootstrap-Asset-Manifest veröffentlicht.', nodeEyebrow: 'Cluster-Knoten', nodeTitle: 'Der Computer, der alles betreibt', nodeDescription: 'Richten Sie diese Konsole auf den Linux-Rechner, der Ihren Cluster betreiben soll. Es wird nichts verändert — er wird nur angesehen, und erst nachdem Sie ihn wiedererkannt haben.', nodeTarget: 'Ziel', nodeRemote: 'Entfernter Linux-Knoten', nodeSameHost: 'Dieser Linux-Launcher-Host', nodeHost: 'Sein Name oder seine Adresse im Netz', nodePort: 'SSH-Port', nodeUsername: 'SSH-Benutzername', nodeAuthentication: 'SSH-Anmeldung', nodeAgent: 'SSH-Agent', nodePrivateKey: 'Privater Schlüssel', nodePassword: 'Passwort', nodeKeyPassphrase: 'Passphrase für privaten Schlüssel', nodeSudoPassword: 'Sudo-Passwort (falls erforderlich)', nodeProbe: 'SSH-Fingerabdruck anzeigen', nodeFingerprint: 'SSH-Hostschlüssel-Fingerabdruck', nodeTrust: 'Bestätigen und vertrauen', nodeInspect: 'Diesen Computer prüfen', nodeOperatingSystem: 'Betriebssystem', nodeCapacity: 'Verfügbare Kapazität', nodeAssessment: 'Bewertung', nodeReady: 'Geeignet — bereit zum Weitermachen', githubEyebrow: 'Git-Anbieter', githubTitle: 'GitHub-Overlay-Zugriff', githubDescription: 'Erstellen Sie ein feingranulares persönliches Zugriffstoken. Es wird nur im verschlüsselten Launcher-Tresor validiert und aufbewahrt.', githubTokenGuide: 'GitHub-Token-Einstellungen öffnen', githubAuthority: 'Berechtigungszweck', githubCreationAuthority: 'Temporäre Repository-Erstellung', githubOngoingAuthority: 'Repository-beschränkter Dauerzugriff', githubToken: 'Feingranulares GitHub-Token', githubValidate: 'Token prüfen und speichern', githubOwner: 'GitHub-Inhaber', githubNoExpiry: 'Kein Ablaufdatum gemeldet', githubRepositoryName: 'Name des neuen privaten Repositorys', githubEstablish: 'GitHub-Overlay einrichten', genericGitEyebrow: 'Vorhandenes Git-Repository', genericGitTitle: 'HTTPS-Git-Overlay-Zugriff', genericGitDescription: 'Verwenden Sie ein leeres HTTPS-Repository. Benutzername und Token werden geprüft, im Launcher-Tresor verschlüsselt und nie an den Browser zurückgegeben.', genericGitUsername: 'Git-Benutzername', genericGitToken: 'Git-Zugriffstoken', genericGitValidate: 'Zugriff prüfen und speichern', genericGitApprovalHint: 'Genehmigen Sie den genauen Änderungsplan unten, bevor dieses leere Repository initialisiert wird.', genericGitEstablish: 'HTTPS-Git-Overlay initialisieren', genericGitPropose: 'Geprüften Änderungszweig übertragen', genericGitManualMerge: 'Prüfen Sie diesen Zweig bei Ihrem Git-Anbieter und führen Sie ihn manuell zusammen:', localBootstrapEyebrow: 'Cluster-Installation', localBootstrapTitle: 'Auf diesem Computer installieren', localBootstrapDescription: 'Prüfen Sie diesen Knoten erneut und kontrollieren Sie die privilegierte, fortsetzbare Installation vor der Genehmigung.', localBootstrapEnvironment: 'Umgebungserweiterung', localBootstrapDataDirectory: 'Wo die Daten Ihrer Gemeinschaft liegen', localBootstrapNodeName: 'Kubernetes-Knotenname', localBootstrapACMEEmail: 'E-Mail für Zertifikate (optional)', localBootstrapManageDNS: 'Öffentliche DNS-Einträge verwalten', localBootstrapSecrets: 'Kubernetes-Secret-Manifeste (außerhalb von Git)', localBootstrapReview: 'Erneut prüfen und Änderungsplan erstellen', localBootstrapOverlayCommit: 'GitOps-Overlay-Commit', handoffEyebrow: 'Verwaltung übergeben', handoffTitle: 'Verwaltung an den Cluster übergeben', handoffDescription: 'Richten Sie vertrauenswürdiges HTTPS und rein privaten Zugang ein, verbinden Sie diesen Computer und übergeben Sie dann die tägliche Verwaltung an die erste Eigentümerin oder den ersten Eigentümer. Nichts davon gelangt ins Internet.', handoffUnlockFirst: 'Öffnen Sie zuerst den Passwort-Tresor — diese Schritte brauchen, was darin liegt.', handoffStepsTitle: 'Fortschritt', handoffStepClusterCA: 'Dieses Gerät vertraut den Zertifikaten Ihres Clusters', handoffStepPrivateNetwork: 'Privates Netzwerk und seine Adressen eingerichtet', handoffStepLauncherEnrolled: 'Dieser Computer ist dem privaten Netzwerk beigetreten', handoffStepGatewayIdentity: 'Der private Zugang hat eine eigene Identität', handoffStepGatewayAccess: 'Es werden nur verschlüsselte Verbindungen angenommen', handoffStepVerified: 'Geprüft: privat erreichbar, Adressen lösen auf, Zertifikate gültig', handoffStepClosed: 'Vorübergehender Direktzugang entfernt', handoffStepFirstOwner: 'Erste Eigentümerin oder erster Eigentümer registriert', handoffClusterCAEstablish: 'Zertifizierungsstelle Ihres Clusters anlegen', handoffDeviceTrustInstall: 'Auf diesem Gerät vertrauen', handoffDeviceTrustFingerprint: 'Zertifikats-Fingerabdruck', handoffBaseDomain: 'Private Webadresse', handoffPrivateNetworkEstablish: 'Privates Netzwerk einrichten', handoffTailscaleDetect: 'Nach der Tailscale-App suchen', handoffTailscaleDetected: 'Die offizielle Tailscale-App ist auf diesem Computer installiert.', handoffTailscaleAbsent: 'Auf diesem Computer wurde keine Tailscale-App gefunden.', handoffTailscaleAcquire: 'Ein geprüfter Download ist verfügbar. Die Installation erfordert Administratorrechte.', handoffTailscaleManual: 'Die offizielle App selbst installieren', handoffEnrollmentEstablish: 'Zugangsdaten für den Beitritt erstellen', handoffLauncherConsume: 'Mit dem Einmal-Zugangsdatum beitreten', handoffVerify: 'Prüfen, ob der private Zugang funktioniert', handoffCloseAccess: 'Vorübergehenden Direktzugang entfernen', handoffFirstOwnerClaim: 'Registrierung der ersten Eigentümerin beginnen', handoffFirstOwnerRegister: 'Passkey registrieren und die Einrichtungstür endgültig schließen', handoffLimitations: 'Was „nur privat“ bedeutet', handoffConsoleUrl: 'Die clustereigene Konsole'
    ,offsiteEyebrow: 'Katastrophenschutz', offsiteTitle: 'Sicherungskopie an einem anderen Ort', offsiteDescription: 'Eine Sicherung auf demselben Rechner geht mit dem Rechner verloren. Richten Sie einen Speicher an einem anderen Ort ein, damit Feuer, Diebstahl oder eine defekte Festplatte nicht das Ende Ihrer Gemeinschaft sind. Die Zugangsschlüssel bleiben im Tresor; nur die Adresse wird notiert.', offsiteEndpoint: 'S3-Endpunkt (HTTPS)', offsiteRegion: 'Region', offsiteBucket: 'Bucket', offsiteAccessKey: 'Zugriffsschlüssel-ID', offsiteSecretKey: 'Geheimer Zugriffsschlüssel', offsiteInspect: 'Ziel prüfen', offsiteReachable: 'Erreichbar', offsiteVersioning: 'Objektversionierung', offsiteFingerprint: 'Fingerabdruck des Zugriffsschlüssels', offsiteVersioningEnabled: 'Aktiviert', offsiteVersioningDisabled: 'Deaktiviert', offsiteVersioningUnsupported: 'Nicht unterstützt', offsiteVersioningUnknown: 'Nicht prüfbar', offsiteAcknowledge: 'Ich bestätige, dass die Objektversionierung nicht bestätigt werden konnte; eine Point-in-Time-Wiederherstellung ist nicht garantiert.', offsitePlanReview: 'Änderungsplan prüfen', offsiteGitDiff: 'Geheimnisfreie Git-Änderung (vorgeschlagen)', offsiteSecretEffect: 'Cluster-Secret (Werte verlassen nie den Tresor)', offsiteSecretKeysLabel: 'Schlüssel', offsiteImplications: 'Was sich ändert', offsiteImplData: 'Eine Kopie aller Buckets wird extern erstellt.', offsiteImplCost: 'Externer Speicher und Egress werden vom Ziel berechnet.', offsiteImplProtection: 'Ermöglicht externen Katastrophenschutz.', offsiteApprovePropose: 'Genehmigen und Git-Vorschlag öffnen', offsiteProposalOpened: 'Vorschlag geöffnet — prüfen und bei Ihrem Git-Anbieter zusammenführen:', offsiteProposalRequired: 'Öffnen und führen Sie den Git-Vorschlag zusammen, bevor Sie validieren.', offsiteValidate: 'Begrenzte Validierung ausführen', offsiteValidationVerdict: 'Validierungsergebnis', offsiteRemediation: 'Empfohlener nächster Schritt', offsiteRecoveryPoint: 'Externer Wiederherstellungspunkt', offsiteResultVerified: 'Externer Schutz bestätigt', offsiteResultLocalBackupFailed: 'Lokale Sicherung fehlgeschlagen', offsiteResultReplicationFailed: 'Replikation zum Ziel fehlgeschlagen', offsiteResultNoEvidence: 'Kein externer Wiederherstellungspunkt beobachtet', offsiteResultStale: 'Externer Wiederherstellungspunkt ist veraltet', offsiteResultVersioningUnsupported: 'Repliziert, aber Versionierung wird nicht unterstützt', offsiteResultPending: 'Validierung ausstehend', offsiteRemediationNone: 'Externer Schutz ist intakt; keine Aktion nötig.', offsiteRemediationLocalBackupFailed: 'Beheben Sie den lokalen Sicherungsjob, bevor Sie die Replikation erneut versuchen.', offsiteRemediationReplicationFailed: 'Prüfen Sie Zugangsdaten und Verbindung des Ziels und führen Sie die Validierung erneut aus.', offsiteRemediationNoEvidence: 'Bestätigen Sie, dass der Replikator lief und in den Ziel-Bucket schrieb.', offsiteRemediationStale: 'Die letzte externe Kopie ist alt; führen Sie die Replikation und Validierung erneut aus.', offsiteRemediationVersioningUnsupported: 'Aktivieren Sie die Objektversionierung am Ziel für Point-in-Time-Wiederherstellung.', offsiteRemediationPending: 'Die Validierung hat noch kein Ergebnis geliefert.'
    ,hetznerEyebrow: 'Hetzner-Infrastruktur', hetznerTitle: 'Hetzner-Infrastruktur prüfen und planen', hetznerDescription: 'Prüfen Sie das Projekt-Token, sehen Sie, was bereits existiert, kontrollieren Sie die DNS-Delegierung und erstellen Sie einen kostenpflichtigen Plan. Bis zur Genehmigung wird nichts in Ihrem Hetzner-Projekt geändert.', hetznerToken: 'Hetzner-Projekt-Token (Lesen und Schreiben)', hetznerTokenValidate: 'Token prüfen und speichern', hetznerTokenGuide: 'Hetzner-Sicherheitseinstellungen des Projekts öffnen', hetznerTokenFingerprint: 'Token-Fingerabdruck', hetznerProject: 'Hetzner-Projekt', hetznerTokenValid: 'Für Lesen und Bereitstellen geprüft.', hetznerTokenMalformed: 'Das ist kein Hetzner-Projekt-Token. Es wurde nichts an den Anbieter gesendet.', hetznerTokenUnauthorized: 'Hetzner hat dieses Token abgelehnt. Erstellen Sie im Projekt ein neues.', hetznerTokenReadOnly: 'Dieses Token kann lesen, aber nicht bereitstellen. Erstellen Sie ein Read-&-Write-Token.', hetznerTokenInconclusive: 'Hetzner konnte gerade nicht antworten — das Token ist weder angenommen noch abgelehnt. Versuchen Sie es gleich erneut.', hetznerTokenProjectMismatch: 'Dieses Token gehört zu einem anderen Hetzner-Projekt als dieses Profil. Die Bereitstellung wurde nicht umgehängt.', hetznerDomain: 'Basis-Domain', hetznerEnvExt: 'Umgebungserweiterung (optional)', hetznerInspect: 'Projekt prüfen', hetznerInventory: 'Was im Projekt existiert', hetznerInspectedAt: 'Geprüft', hetznerAdoptSelected: 'Diese Ressource übernehmen', hetznerOwnershipShared: 'Profilübergreifend geteilt', hetznerOwnershipProfileOwned: 'Gehört diesem Profil', hetznerOwnershipAdoptable: 'Vorhanden — Übernahme muss entschieden werden', hetznerOwnershipConflicting: 'Gehört einem anderen Profil', hetznerOwnershipUnknown: 'Ähnlich benannt — wird nie automatisch übernommen', hetznerOwnershipAbsent: 'Wird erstellt', hetznerSimilarNames: 'Ähnlich benannte Ressourcen gefunden', hetznerDelegation: 'Nameserver-Delegierung', hetznerDelegationConfirmed: 'Die Domain ist an Hetzner delegiert.', hetznerDelegationPartial: 'Nur ein Teil der Nameserver zeigt auf Hetzner; DNS und Zertifikate wären unzuverlässig.', hetznerDelegationMissing: 'Die Domain ist anderswo delegiert. Richten Sie sie beim Registrar auf Hetzner aus.', hetznerDelegationUnknown: 'Die Delegierung konnte noch nicht geprüft werden; eine öffentliche Installation bleibt blockiert.', hetznerDelegationNotRequired: 'Für eine reine LAN-Installation nicht erforderlich.', hetznerExpectedNameservers: 'Erwartete Nameserver', hetznerObservedNameservers: 'Derzeit veröffentlichte Nameserver', hetznerCapacity: 'Kapazität und Kosten', hetznerPresetSmall: 'Klein', hetznerPresetRecommended: 'Empfohlen', hetznerPresetHigh: 'Hohe Kapazität', hetznerPresetAdvanced: 'Erweitert', hetznerRequirement: 'Von Ihren gewählten Fähigkeiten benötigt', hetznerLocation: 'Standort', hetznerServerType: 'Servertyp', hetznerVolume: 'Datenvolume', hetznerMonthlyCost: 'Geschätzte monatliche Kosten', hetznerPricesObservedAt: 'Preise von Hetzner gelesen am', hetznerPresetUnavailable: 'Derzeit an diesem Standort nicht verfügbar.', hetznerPresetTooSmall: 'Zu klein für die von Ihnen gewählten Fähigkeiten.', hetznerAdvancedHint: 'Erweitert: Standort, Servertyp und Volumegröße selbst wählen.', hetznerCostNoteVolumeGrows: 'Ein Volume kann später vergrößert, aber nie verkleinert werden.', hetznerCostNoteVolumeBillable: 'Das Volume bleibt bis zum Löschen kostenpflichtig, auch ohne Server.', hetznerCostNotePrimaryIP: 'Eine reservierte Primary IP bleibt kostenpflichtig, solange sie existiert.', hetznerCostNoteSnapshots: 'Snapshots und Backups werden separat berechnet.', hetznerCostNoteVAT: 'Preise ohne Mehrwertsteuer und ohne zusätzlichen Traffic.', hetznerCostNoteObserved: 'Geschätzt aus dem beim Planen gelesenen Anbieterkatalog.', hetznerToolchainTitle: 'Infrastruktur-Werkzeuge', hetznerToolchainDescription: 'Das fixierte OpenTofu und der Hetzner-Provider werden geprüft in den privaten Launcher-Speicher geladen. Global installierte Werkzeuge werden nicht verwendet.', hetznerToolchainAcquire: 'Werkzeuge laden und prüfen', hetznerToolchainReady: 'Geprüft', hetznerToolchainPending: 'Noch nicht geladen', hetznerToolchainUnavailable: 'Für diese Plattform sind noch keine geprüften Werkzeuge veröffentlicht. Der Launcher greift nicht auf ein Werkzeug dieses Computers zurück.', hetznerWorkspace: 'Status-Arbeitsbereich', hetznerWorkspaceIsolated: 'Für dieses Profil isoliert', hetznerWorkspaceLocked: 'Gesperrt von', hetznerWorkspaceBackups: 'Aufbewahrte Status-Sicherungen', hetznerPlanBuild: 'Änderungsplan erstellen', hetznerPlanTitle: 'Infrastruktur-Änderungsplan', hetznerPlanItems: 'Geplante Ressourcen', hetznerPlanBlockers: 'Vor der Genehmigung klären', hetznerPlanApprovable: 'Genehmigungsbereit. Die Genehmigung ist der erste Schritt, der Ihr Hetzner-Projekt ändern kann.', hetznerApprove: 'Plan genehmigen', hetznerActionCreate: 'Erstellen', hetznerActionAdopt: 'Übernehmen', hetznerActionReuseShared: 'Mitbenutzen (geteilt)', hetznerActionKeep: 'Behalten', hetznerActionBlocked: 'Blockiert', hetznerBlockerAdoption: 'Eine vorhandene Ressource muss ausdrücklich übernommen oder umbenannt werden.', hetznerBlockerConflict: 'Eine Ressource dieses Namens gehört einem anderen Clusterprofil.', hetznerBlockerSimilar: 'Es existiert eine ähnlich benannte Ressource; klären Sie das zuerst im Hetzner-Projekt.', hetznerBlockerDelegation: 'Delegieren Sie die Domain vor einer öffentlichen Installation an Hetzner.', hetznerBlockerUnavailable: 'Der gewählte Servertyp kann an diesem Standort derzeit nicht erstellt werden.', hetznerBlockerCapacity: 'Die gewählte Kapazität liegt unter dem Bedarf Ihrer gewählten Fähigkeiten.', hetznerBlockerIncomplete: 'Die Prüfung wurde nicht abgeschlossen; prüfen Sie erneut, bevor Sie planen.', hetznerPlanStale: 'Das Projekt hat sich seit diesem Plan geändert. Prüfen und planen Sie erneut.', hetznerBlockerSharedPrerequisite: 'Die DNS-Zone und der geteilte Admin-SSH-Schlüssel gehören zum gesamten Projekt. Legen Sie sie zuerst in Hetzner an; diese Installation benutzt sie mit und besitzt sie nie.', hetznerAcmeEmail: 'Adresse für Zertifikatskonto', hetznerAcmeEmailHint: "Let's Encrypt verwendet diese Adresse für Ablaufwarnungen zu den Zertifikaten Ihrer Gemeinschaft. Sie wird als gewöhnliche Kontaktangabe gespeichert, nicht als Zugangsdatum.", hetznerAccessTitle: 'Vorübergehender Verwaltungszugang', hetznerAccessDescription: 'Während der Cluster entsteht, sind SSH und die Kubernetes-API aus dem Internet erreichbar, damit Sie den Aufbau verfolgen können. Dieser Zugang wird entfernt, sobald die private Verwaltung geprüft ist — nie vorher, denn er ist Ihr einziger Rückweg, falls etwas schiefgeht.', hetznerAccessState: 'Zugang', hetznerAccessOpen: 'Offen', hetznerAccessClosed: 'Entfernt', hetznerAccessScope: 'Erreichbar von', hetznerAccessUnscoped: 'Von überall im Internet', hetznerAccessReason: 'Warum', hetznerAccessAddress: 'Ihre öffentliche Adresse', hetznerAccessAddressHint: 'Den Zugang auf Ihre eigene Adresse zu beschränken ist sicherer — aber nur, wenn diese Adresse stabil ist. Ändert sie sich, etwa im Mobilfunk oder bei einem Anschluss mit wechselnder Adresse, sperren Sie sich selbst aus.', hetznerAccessNarrow: 'Auf diese Adresse beschränken', hetznerAccessReasonScoped: 'Auf Ihre Adresse allein beschränkt.', hetznerAccessReasonUnobserved: 'Ihre öffentliche Adresse ist noch nicht bekannt, daher bleibt der Zugang offen.', hetznerAccessReasonNotRoutable: 'Das ist eine lokale Adresse, nicht die, die Hetzner sieht. Eine Beschränkung darauf würde alle aussperren, daher bleibt der Zugang offen.', hetznerAccessReasonShared: 'Diese Adresse teilen Sie sich mit anderen Kunden Ihres Anbieters, und sie wechselt ohne Vorwarnung. Eine Beschränkung darauf wäre schwächer als sie aussieht und zugleich riskant, daher bleibt der Zugang offen.'
    ,journeyProgress: 'Einrichtungsfortschritt'
    ,stepDone: 'Erledigt'
    ,stepCurrent: 'Sie sind hier'
    ,stepLocked: 'Noch nicht'
    ,stepChange: 'Ändern'
    ,stepOf: 'Schritt {n} von {total}'
    ,stepCapabilitiesTitle: 'Wählen Sie, was Ihre Gemeinschaft bekommt'
    ,stepCapabilitiesSummary: 'Wählen Sie die Anwendungen für Ihre Mitglieder und die Webadresse, die sie aufrufen.'
    ,stepAssetsTitle: 'Installationsdateien herunterladen'
    ,stepAssetsSummary: 'Lädt und prüft genau die Dateien, die diese Version braucht. Es wird noch nichts installiert.'
    ,stepNodeTitle: 'Wählen Sie den Computer, der alles betreibt'
    ,stepNodeSummary: 'Richten Sie die Konsole auf den Rechner, prüfen Sie seine Eignung und installieren Sie darauf.'
    ,stepHetznerTitle: 'Server mieten und vorbereiten'
    ,stepHetznerSummary: 'Prüfen Sie Ihr Hosting-Konto, wählen Sie eine Größe und sehen Sie die Kosten, bevor etwas angelegt wird.'
    ,stepSettingsRepoTitle: 'Wählen Sie, wo Ihre Einstellungen liegen'
    ,stepSettingsRepoSummary: 'Ihr Cluster liest seine Einstellungen aus einem privaten Git-Repository. Legen Sie ein neues an oder nutzen Sie ein vorhandenes.'
    ,stepHandoffTitle: 'Verwaltung an den Cluster übergeben'
    ,stepHandoffSummary: 'Verlagert die tägliche Verwaltung von diesem Computer auf den Cluster selbst.'
    ,stepProtectTitle: 'Gegen den Verlust des Rechners schützen'
    ,stepProtectSummary: 'Kopiert Sicherungen an einen anderen Ort, damit ein defekter Rechner nicht die Gemeinschaft kostet.'
    ,stepBlockedChooseFirst: 'Wählen Sie zuerst, was Ihre Gemeinschaft bekommt.'
    ,stepBlockedInstallersFirst: 'Laden Sie zuerst die Installationsdateien herunter.'
    ,stepBlockedMachineFirst: 'Machen Sie zuerst den Computer bereit.'
    ,stepBlockedRepositoryFirst: 'Wählen Sie zuerst, wo Ihre Einstellungen liegen.'
    ,settingsRepoChoice: 'Wo sollen Ihre Einstellungen liegen?'
    ,settingsRepoGitHub: 'Neues privates Repository auf GitHub anlegen'
    ,settingsRepoGeneric: 'Ein vorhandenes Git-Repository nutzen'
    ,retireTitle: 'Diese Installation stilllegen'
    ,retireDescription: 'Nur nötig, wenn Sie den Cluster außer Betrieb nehmen. Kein Teil der Einrichtung.'
    ,retireShow: 'Stilllegungsoptionen anzeigen'
    ,retireHide: 'Stilllegungsoptionen ausblenden'

    ,secretAlreadySaved: 'Bereits gespeichert. Leer lassen, um es zu behalten, oder neu eingeben, um es zu ersetzen.'

    ,foreignInstallFound: 'Auf diesem Computer ist bereits etwas anderes installiert, das im Weg ist. SmallWorlds installiert nicht ohne Ihre Zustimmung darüber.'
    ,foreignInstallRemove: 'Entfernen, was im Weg ist'
    ,foreignInstallRemoving: 'Wird entfernt …'
    ,nodeSSHKeyPlanned: 'Schlüssel geplant'

  }
} as const;

export type MessageKey = keyof (typeof messages)['en'];

export function translate(locale: Locale, key: MessageKey): string {
  return messages[locale][key];
}
