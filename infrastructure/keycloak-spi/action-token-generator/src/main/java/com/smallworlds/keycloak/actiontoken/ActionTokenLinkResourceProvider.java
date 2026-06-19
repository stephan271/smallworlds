package com.smallworlds.keycloak.actiontoken;

import org.keycloak.models.KeycloakSession;
import org.keycloak.services.resource.RealmResourceProvider;

public class ActionTokenLinkResourceProvider implements RealmResourceProvider {

    private final KeycloakSession session;

    public ActionTokenLinkResourceProvider(KeycloakSession session) {
        this.session = session;
    }

    @Override
    public Object getResource() {
        return new ActionTokenLinkResource(session);
    }

    @Override
    public void close() {
    }
}
