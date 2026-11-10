package main

// runJob is the periodic background job. It keeps ghost profiles in step
// with Mattermost, since no plugin hook reports profile changes.
func (p *Plugin) runJob() {
	p.logger.LogDebug("Background job running")
	p.sweepProfiles()
	p.sweepCalls()
	p.reconcileSome()
	p.sweepSentEvents()
	p.sweepChannelStates()
	p.syncSomeChannelRoles()
}
