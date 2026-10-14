package com.smallworlds.keycloak.actiontoken;

import jakarta.ws.rs.core.Response;

import org.keycloak.TokenVerifier.Predicate;
import org.keycloak.authentication.actiontoken.AbstractActionTokenHandler;
import org.keycloak.authentication.actiontoken.ActionTokenContext;
import org.keycloak.authentication.actiontoken.TokenUtils;
import org.keycloak.authentication.requiredactions.util.RequiredActionsValidator;
import org.keycloak.events.Errors;
import org.keycloak.events.EventType;
import org.keycloak.protocol.oidc.OIDCLoginProtocol;
import org.keycloak.protocol.oidc.utils.RedirectUtils;
import org.keycloak.services.managers.AuthenticationManager;
import org.keycloak.services.messages.Messages;
import org.keycloak.sessions.AuthenticationSessionModel;

/**
 * Runs a Member Invitation's required actions and then signs the member in.
 *
 * Modelled on Keycloak 26's ExecuteActionsActionTokenHandler, with two
 * deliberate differences (docs/adr/0059):
 *
 * 1. No confirmation page. Keycloak shows "perform the following actions" when
 *    the link is opened in a browser without an authentication session, so that
 *    nobody can be steered into acting on an account through a link someone
 *    else sent them. Here the link is never sent: join mints it and hands it to
 *    the browser that has just entered the invitation code and seen the
 *    username, and the passkey page names the account again.
 * 2. No "account updated" page. The base handler marks every fresh session
 *    END_AFTER_REQUIRED_ACTIONS; removing that mark lets Keycloak finish with a
 *    normal sign-in and an OIDC redirect to the join page's /done/<lang>, so the
 *    member arrives signed in. Keycloak's own InviteOrgActionTokenHandler does
 *    the same.
 *
 * Single use: Keycloak invalidates the token once the actions are done. join
 * mints a fresh one whenever the invitation is opened again.
 */
public class JoinActionTokenHandler extends AbstractActionTokenHandler<JoinActionToken> {

    public JoinActionTokenHandler() {
        super(
            JoinActionToken.TOKEN_TYPE,
            JoinActionToken.class,
            Messages.INVALID_CODE,
            EventType.EXECUTE_ACTIONS,
            Errors.NOT_ALLOWED
        );
    }

    @Override
    @SuppressWarnings("unchecked")
    public Predicate<? super JoinActionToken>[] getVerifiers(ActionTokenContext<JoinActionToken> tokenContext) {
        return TokenUtils.predicates(
            // join always names its own /done/<lang>, which must be registered
            // on the `join` client.
            TokenUtils.checkThat(
                t -> t.getRedirectUri() != null
                     && RedirectUtils.verifyRedirectUri(tokenContext.getSession(), t.getRedirectUri(),
                            tokenContext.getAuthenticationSession().getClient()) != null,
                Errors.INVALID_REDIRECT_URI,
                Messages.INVALID_REDIRECT_URI
            ),
            TokenUtils.checkThat(
                t -> RequiredActionsValidator.validRequiredActions(tokenContext.getSession(), t.getRequiredActions()),
                Errors.RESOLVE_REQUIRED_ACTIONS,
                Messages.INVALID_TOKEN_REQUIRED_ACTIONS
            )
        );
    }

    @Override
    public Response handleToken(JoinActionToken token, ActionTokenContext<JoinActionToken> tokenContext) {
        AuthenticationSessionModel authSession = tokenContext.getAuthenticationSession();

        String redirectUri = RedirectUtils.verifyRedirectUri(tokenContext.getSession(), token.getRedirectUri(),
                authSession.getClient());
        authSession.setRedirectUri(redirectUri);
        authSession.setClientNote(OIDCLoginProtocol.REDIRECT_URI_PARAM, redirectUri);

        // Finish with a sign-in rather than Keycloak's "account updated" page.
        authSession.removeAuthNote(AuthenticationManager.END_AFTER_REQUIRED_ACTIONS);
        authSession.removeAuthNote(AuthenticationManager.SET_REDIRECT_URI_AFTER_REQUIRED_ACTIONS);

        token.getRequiredActions().forEach(authSession::addRequiredAction);

        String nextAction = AuthenticationManager.nextRequiredAction(tokenContext.getSession(), authSession,
                tokenContext.getRequest(), tokenContext.getEvent());
        return AuthenticationManager.redirectToRequiredActions(tokenContext.getSession(), tokenContext.getRealm(),
                authSession, tokenContext.getUriInfo(), nextAction);
    }

    @Override
    public boolean canUseTokenRepeatedly(JoinActionToken token, ActionTokenContext<JoinActionToken> tokenContext) {
        return false;
    }
}
