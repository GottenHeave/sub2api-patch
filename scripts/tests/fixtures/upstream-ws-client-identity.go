	}
	accountScopedFirst, accountScoped, scopeErr := applyCodexAccountIdentityClientMetadataRaw(firstClientMessage, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c), codexClientSessionID(c))
	if scopeErr != nil {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket identity metadata", scopeErr)
	}
	if accountScoped {
		firstClientMessage = accountScopedFirst
	}
	usageMeta := newOpenAIWSPassthroughUsageMeta(initialRequestModel, firstClientMessage)
	updatedFirst, blocked, policyErr := s.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, capturedSessionModel, firstClientMessage)
	if policyErr != nil {
