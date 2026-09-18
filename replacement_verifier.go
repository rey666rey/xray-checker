package main

import (
	"context"
	"fmt"

	"xray-checker/checker"
	"xray-checker/models"
	"xray-checker/web"
)

type replacementVerifier struct {
	proxyChecker *checker.ProxyChecker
	updater      *subscriptionUpdater
}

func (verifier *replacementVerifier) Verify(ctx context.Context, stableID string) (web.ReplacementVerification, error) {
	result := web.ReplacementVerification{PreviousStableID: stableID}
	if verifier.updater == nil {
		return result, fmt.Errorf("subscription refresh is disabled")
	}

	err := verifier.proxyChecker.WithManualPriority(func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		previous, ok := verifier.proxyChecker.GetProxyByStableID(stableID)
		if !ok {
			return fmt.Errorf("binding not found")
		}
		previousLogicalID := previous.LogicalID
		previousHostID := previous.HostID
		previousRevision := previous.GenerateRevisionID()
		result.PreviousAddress = proxyEndpoint(previous)

		plan, err := verifier.updater.Refresh()
		if err != nil {
			return err
		}

		target, replacementDetected, candidates := verifier.findReplacement(
			previous, previousLogicalID, previousHostID, previousRevision, plan,
		)
		if target == nil {
			result.Candidates = candidates
			if candidates > 1 {
				result.State = web.ReplacementAmbiguous
				result.Message = "Several active replacement candidates were received; choose the new card to verify it"
				return nil
			}
			result.State = web.ReplacementNotReceived
			result.Message = "The replacement has not been received from the subscription yet"
			return nil
		}

		result.StableID = target.StableID
		result.CurrentAddress = proxyEndpoint(target)
		result.ReplacementDetected = replacementDetected
		firstOnline, firstUnstable, _, _, firstFound := verifier.proxyChecker.GetProxyResultDetailsByStableID(target.StableID)
		if err := verifier.proxyChecker.RecheckProxy(target.StableID); err != nil {
			return err
		}

		online, unstable, _, _, found := verifier.proxyChecker.GetProxyResultDetailsByStableID(target.StableID)
		if !found {
			return fmt.Errorf("replacement result is unavailable")
		}
		monitor, _ := verifier.proxyChecker.GetNodeMonitorByStableID(target.StableID)
		result.Online = online
		result.Unstable = unstable
		result.MonitorState = string(monitor.State)
		switch {
		case replacementDetected && online && (!firstFound || !firstOnline || firstUnstable || unstable):
			result.Unstable = true
			result.State = web.ReplacementUnstable
			result.Message = "The replacement responded, but still needs another clean confirmation"
		case online && unstable:
			result.State = web.ReplacementUnstable
			result.Message = "The replacement works intermittently and needs another confirmation"
		case online && replacementDetected:
			result.State = web.ReplacementVerified
			result.Message = "The replacement was received and passed verification"
		case online:
			result.State = web.ReplacementCurrentVerified
			result.Message = "The active server passed verification"
		case replacementDetected:
			result.State = web.ReplacementFailed
			result.Message = "The replacement was received but failed verification"
		default:
			result.State = web.ReplacementFailed
			result.Message = "The active server failed verification"
		}
		return nil
	})
	return result, err
}

func (verifier *replacementVerifier) findReplacement(previous *models.ProxyConfig, logicalID, hostID, revision string, plan checker.ProxyUpdatePlan) (*models.ProxyConfig, bool, int) {
	proxies := verifier.proxyChecker.GetProxies()
	var current *models.ProxyConfig
	for _, proxy := range proxies {
		if proxy.StableID == previous.StableID {
			current = proxy
		}
		if logicalID != "" && proxy.LogicalID == logicalID && proxy.GenerateRevisionID() != revision {
			return proxy, true, 1
		}
	}

	// If this forced refresh discovered a new binding for the same subscription
	// host, it is stronger evidence than the accumulated endpoint pool. This is
	// the common operator workflow: replace the server in the panel, then click
	// Re-check while older rotating endpoints are still valid members of the pool.
	fresh := make([]*models.ProxyConfig, 0)
	for _, change := range plan.Changes {
		if change.Kind != checker.ProxyAdded || change.New == nil ||
			change.New.StableID == previous.StableID || hostID == "" || change.New.HostID != hostID {
			continue
		}
		observation, ok := verifier.proxyChecker.GetEndpointObservation(change.New)
		if !ok || observation.MissingPolls == 0 {
			fresh = append(fresh, change.New)
		}
	}
	if len(fresh) == 1 {
		return fresh[0], true, 1
	}
	if len(fresh) > 1 {
		return nil, false, len(fresh)
	}

	alternatives := make([]*models.ProxyConfig, 0)
	for _, proxy := range proxies {
		if proxy.StableID == previous.StableID || hostID == "" || proxy.HostID != hostID {
			continue
		}
		observation, ok := verifier.proxyChecker.GetEndpointObservation(proxy)
		if !ok || observation.MissingPolls == 0 {
			alternatives = append(alternatives, proxy)
		}
	}
	if len(alternatives) == 1 {
		return alternatives[0], true, 1
	}
	if len(alternatives) > 1 {
		return nil, false, len(alternatives)
	}

	if current == nil {
		return nil, false, 0
	}
	observation, observed := verifier.proxyChecker.GetEndpointObservation(current)
	monitor, _ := verifier.proxyChecker.GetNodeMonitorByStableID(current.StableID)
	if (observed && observation.MissingPolls > 0) ||
		monitor.State == checker.NodeUnknown || monitor.State == checker.NodeUnstable ||
		monitor.State == checker.NodeSuspected || monitor.State == checker.NodeNeedsReplacement ||
		monitor.State == checker.NodeNewIPFailed {
		return nil, false, 0
	}
	return current, false, 0
}

func proxyEndpoint(proxy *models.ProxyConfig) string {
	if proxy == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", proxy.Server, proxy.Port)
}
