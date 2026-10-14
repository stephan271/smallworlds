package com.smallworlds.keycloak.actiontoken;

import java.util.LinkedList;
import java.util.List;

import org.keycloak.authentication.actiontoken.DefaultActionToken;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * The action token behind a Member Invitation (docs/adr/0058, 0059).
 *
 * Carries the same fields as Keycloak's execute-actions token, but has a type
 * of its own so that {@link JoinActionTokenHandler} — not Keycloak's handler —
 * processes it. That handler skips the confirmation page and signs the member
 * in at the end instead of stopping on "account updated".
 */
public class JoinActionToken extends DefaultActionToken {

    public static final String TOKEN_TYPE = "smallworlds-join";

    private static final String JSON_FIELD_REQUIRED_ACTIONS = "rqac";
    private static final String JSON_FIELD_REDIRECT_URI = "reduri";

    @JsonProperty(JSON_FIELD_REQUIRED_ACTIONS)
    private List<String> requiredActions;

    @JsonProperty(JSON_FIELD_REDIRECT_URI)
    private String redirectUri;

    public JoinActionToken(String userId, int absoluteExpirationInSecs, List<String> requiredActions,
                           String redirectUri, String clientId) {
        super(userId, TOKEN_TYPE, absoluteExpirationInSecs, null);
        this.requiredActions = requiredActions == null ? new LinkedList<>() : new LinkedList<>(requiredActions);
        this.redirectUri = redirectUri;
        this.issuedFor = clientId;
    }

    private JoinActionToken() {
        // for Jackson
    }

    public List<String> getRequiredActions() {
        return requiredActions;
    }

    public String getRedirectUri() {
        return redirectUri;
    }
}
