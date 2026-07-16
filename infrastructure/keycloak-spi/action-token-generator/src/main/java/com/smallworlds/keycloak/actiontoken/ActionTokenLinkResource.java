package com.smallworlds.keycloak.actiontoken;

import org.keycloak.authentication.actiontoken.execactions.ExecuteActionsActionToken;
import org.keycloak.common.util.Time;
import org.keycloak.models.KeycloakSession;
import org.keycloak.models.RealmModel;
import org.keycloak.models.UserModel;
import org.keycloak.services.managers.AppAuthManager;
import org.keycloak.services.managers.AuthenticationManager;
import org.keycloak.services.resources.admin.AdminAuth;
import org.keycloak.services.Urls;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.UriBuilder;
import jakarta.ws.rs.core.UriInfo;
import jakarta.ws.rs.NotAuthorizedException;
import jakarta.ws.rs.NotFoundException;

import java.util.List;
import java.util.Map;

public class ActionTokenLinkResource {

    private final KeycloakSession session;

    public ActionTokenLinkResource(KeycloakSession session) {
        this.session = session;
    }

    @POST
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    @Path("generate-link")
    public Response generateLink(Map<String, Object> requestBody) {
        AuthenticationManager.AuthResult authResult = new AppAuthManager.BearerTokenAuthenticator(session).authenticate();
        if (authResult == null) {
            throw new NotAuthorizedException("Bearer");
        }

        RealmModel realm = session.getContext().getRealm();
        AdminAuth auth = new AdminAuth(realm, authResult.getToken(), authResult.getUser(), authResult.getClient());
        
        // Fallback admin check for Keycloak 26 since AdminPermissions is no longer exposed here
        org.keycloak.models.ClientModel realmManagementClient = session.clients().getClientByClientId(realm, "realm-management");
        if (!auth.hasRealmRole("admin") && !auth.hasOneOfAppRole(realmManagementClient, "manage-users")) {
            throw new jakarta.ws.rs.ForbiddenException("Requires admin or manage-users role");
        }

        String userId = (String) requestBody.get("userId");
        String redirectUri = (String) requestBody.get("redirectUri");
        String clientId = (String) requestBody.get("clientId");
        List<String> actions = (List<String>) requestBody.get("actions");

        if (userId == null || actions == null || actions.isEmpty()) {
            return Response.status(Response.Status.BAD_REQUEST).entity("{\"error\":\"userId and actions are required\"}").build();
        }

        UserModel user = session.users().getUserById(realm, userId);
        if (user == null) {
            throw new NotFoundException("User not found");
        }

        int lifespan = realm.getActionTokenGeneratedByAdminLifespan();
        int expiration = Time.currentTime() + lifespan;

        ExecuteActionsActionToken token = new ExecuteActionsActionToken(user.getId(), expiration, actions, redirectUri, clientId);

        UriInfo uriInfo = session.getContext().getUri();
        UriBuilder builder = Urls.actionTokenBuilder(uriInfo.getBaseUri(), token.serialize(session, realm, uriInfo), clientId, "", "");
        String link = builder.build(realm.getName()).toString();

        return Response.ok(Map.of("link", link)).build();
    }
}
