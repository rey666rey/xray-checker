// Test the rendered model without external services or browser state.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const html = fs.readFileSync(process.argv[2], 'utf8');
const script = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map(m => m[1]).find(s => s.includes('function dashboard()'));
const store = new Map();
let proxiesSuccess = false;
const factory = vm.runInNewContext('(' + script.slice(script.indexOf('function dashboard()')).trim() + ')', {
  localStorage: { getItem: key => store.get(key) ?? null, setItem: (key, value) => store.set(key, value) },
  getUrlParam: () => false, URLSearchParams, window: { location: { search: '' } }, console,
  fetch: async url => ({ ok: true, json: async () => url.endsWith('/proxies')
    ? { success: proxiesSuccess, data: [] } : { success: true, data: url.endsWith('/nodes') ? [] : null } }),
});
(async () => {
  const app = factory();
  app.$nextTick = () => {};
  for (const name of ['MASTER', '220V', 'LETO', 'DOG']) {
    assert.equal(app.isMonitorPanelCollapsed(name), false);
    app.toggleMonitorPanel(name);
    assert.equal(app.isMonitorPanelCollapsed(name), true);
  }
  app.toggleMonitorPanel('LETO');
  assert.equal(app.isMonitorPanelCollapsed('MASTER'), true);
  assert.equal(app.isMonitorPanelCollapsed('LETO'), false);
  assert.equal(factory().isMonitorPanelCollapsed('MASTER'), true);
  const state = app.panelState('MASTER');
  Object.assign(state, { search: 'Узел', filter: 'online', sort: 'latency-asc', page: 2 });
  const beforeCollapse = JSON.parse(JSON.stringify(state));
  app.toggleMonitorPanel('MASTER');
  app.toggleMonitorPanel('MASTER');
  assert.deepEqual(JSON.parse(JSON.stringify(app.panelState('MASTER'))), beforeCollapse);
  app.openMonitorPanel('MASTER');
  assert.equal(app.isMonitorPanelCollapsed('MASTER'), false);
  assert.equal(app.expandedPanel, 'MASTER');
  assert.notEqual(app.monitorPanelBodyId('MASTER'), app.monitorPanelBodyId('LETO'));
  for (const value of ['bad-json', '[]', 'null']) {
    store.set('monitorCollapsedPanels', value);
    assert.equal(factory().isMonitorPanelCollapsed('MASTER'), false);
  }
  store.set('monitorCollapsedPanels', '{"MASTER":"yes","LETO":true}');
  assert.equal(factory().isMonitorPanelCollapsed('MASTER'), false);
  assert.equal(factory().isMonitorPanelCollapsed('LETO'), true);
  let copied, toast;
  app.copyText = async text => { copied = text; };
  app.showToast = text => { toast = text; };
  for (const [proxy, expected] of [
    [{ serverInfo: '192.0.2.4:443' }, '192.0.2.4'],
    [{ serverInfo: '[2001:db8::2]:443' }, '2001:db8::2'],
    [{ serverInfo: 'host.test:443', currentResolved: '2001:db8::9' }, '2001:db8::9'],
    [{ currentResolved: '192.0.2.8, 192.0.2.9' }, '192.0.2.8'],
    [{ serverInfo: 'host.test:443' }, 'host.test'],
  ]) {
    await app.copyIp(proxy);
    assert.equal(copied, expected);
    assert.equal(toast, 'IP скопирован: ' + expected);
  }
  copied = undefined;
  await app.copyIp({});
  assert.equal(copied, undefined);
  assert.equal(toast, 'IP отсутствует');
  for (const [n, expected] of [[0,'0 узлов'],[1,'1 узел'],[2,'2 узла'],[11,'11 узлов'],[21,'21 узел'],[24,'24 узла'],[111,'111 узлов']]) {
    assert.equal(app.monitorNodeCountLabel(n), expected);
  }
  assert.equal(app.formatMonitorSyncTime(Date.parse('2026-10-17T23:18:00Z')), '23:18');
  app.lastSyncedAt = 1;
  await app.refreshData();
  assert.equal(app.lastSyncedAt, 1);
  proxiesSuccess = true;
  await app.refreshData();
  assert(app.lastSyncedAt > 1);
  console.log('FINISHING_STATE_PASS: footer/copy-ip/collapse/persistence=PASS');
})().catch(err => { console.error(err); process.exitCode = 1; });
