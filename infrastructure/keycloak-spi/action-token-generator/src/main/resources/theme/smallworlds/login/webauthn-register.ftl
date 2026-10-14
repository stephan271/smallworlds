<#--
  Copied from Keycloak 26.6.4, theme/keycloak.v2/login/webauthn-register.ftl
  (Apache License 2.0), with three SmallWorlds changes (docs/adr/0059):
  - the page names the account the passkey is for, and says what happens next.
    Invitations skip Keycloak's confirmation page, so this is where a member
    sees whose account they are acting on;
  - no "sign out from other devices" checkbox: a new member has no other
    sessions, and the Invitation Desk ends them when it removes passkeys;
  - resources/js/webauthnRegister.js is this theme's copy, which asks no
    question for the passkey's name.
  Diff against the new keycloak.v2 file on every Keycloak upgrade.
-->
<#import "template.ftl" as layout>
<#import "buttons.ftl" as buttons>

<@layout.registrationLayout; section>
    <#if section = "title">
        title
    <#elseif section = "header">
        ${msg("webauthn-registration-title")}
    <#elseif section = "form">
    <div class="${properties.kcFormClass!}">
        <div class="sw-account">
            <span class="sw-account-label">${msg("swPasskeyAccount")}</span>
            <strong class="sw-account-name">${username}</strong>
        </div>
        <p class="sw-explain">${msg("swPasskeyExplain")}</p>
        <form id="register" action="${url.loginAction}" method="post" >
            <div class="${properties.kcFormGroupClass!}">
                <input type="hidden" id="clientDataJSON" name="clientDataJSON"/>
                <input type="hidden" id="attestationObject" name="attestationObject"/>
                <input type="hidden" id="publicKeyCredentialId" name="publicKeyCredentialId"/>
                <input type="hidden" id="authenticatorLabel" name="authenticatorLabel"/>
                <input type="hidden" id="transports" name="transports"/>
                <input type="hidden" id="authenticatorAttachment" name="authenticatorAttachment"/>
                <input type="hidden" id="error" name="error"/>
            </div>
        </form>

        <script type="module">
            <#outputformat "JavaScript">
            import { registerByWebAuthn } from "${url.resourcesPath}/js/webauthnRegister.js";
            const registerButton = document.getElementById('registerWebAuthn');
            registerButton.addEventListener("click", function() {
                const input = {
                    challenge : ${challenge?c},
                    userid : ${userid?c},
                    username : ${username?c},
                    signatureAlgorithms : [<#list signatureAlgorithms as sigAlg>${sigAlg?c},</#list>],
                    rpEntityName : ${rpEntityName?c},
                    rpId : ${rpId?c},
                    attestationConveyancePreference : ${attestationConveyancePreference?c},
                    authenticatorAttachment : ${authenticatorAttachment?c},
                    requireResidentKey : ${requireResidentKey?c},
                    userVerificationRequirement : ${userVerificationRequirement?c},
                    createTimeout : ${createTimeout?c},
                    excludeCredentialIds : ${excludeCredentialIds?c},
                    initLabel : ${msg("webauthn-registration-init-label")?c},
                    initLabelPrompt : ${msg("webauthn-registration-init-label-prompt")?c},
                    errmsg : ${msg("webauthn-unsupported-browser-text")?c}
                };
                registerByWebAuthn(input);
            },  { once: true });
            </#outputformat>
        </script>

            <@buttons.actionGroup horizontal=true>
                <@buttons.button id="registerWebAuthn" label="doRegisterSecurityKey" class=["kcButtonPrimaryClass","kcButtonBlockClass"]/>
                <#if !isSetRetry?has_content && isAppInitiatedAction?has_content>
                    <form class="${properties.kcFormClass!}" action="${url.loginAction}"
                          id="kc-webauthn-settings-form" method="post">
                        <@buttons.button id="cancelWebAuthnAIA" name="cancel-aia" label="doCancel" class=["kcButtonSecondaryClass","kcButtonBlockClass"]/>
                    </form>
                </#if>
            </@buttons.actionGroup>
    </div>
    </#if>
</@layout.registrationLayout>
