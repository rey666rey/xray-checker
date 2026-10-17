const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const html = fs.readFileSync(process.argv[2], 'utf8');
const script = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map(m => m[1]).find(s => s.includes('function dashboard()'));
const factory = vm.runInNewContext('(' + script.slice(script.indexOf('function dashboard()')).trim() + ')', {
  localStorage: { getItem: () => null, setItem() {} }, getUrlParam: () => false,
  URLSearchParams, window: { location: { search: '' } }, console,
});
const app = factory();
app.$nextTick = () => {};
const base = app.proxies[0];
const names = ['DOG', 'MASTER', 'LETO', '220V'];
function permutations(values) {
  return values.length ? values.flatMap((value, index) => permutations(values.filter((_, i) => i !== index)).map(rest => [value, ...rest])) : [[]];
}
for (const order of permutations(names)) {
  app.proxies = order.map((subName, i) => ({ ...base, subName, stableId: 'test-' + i }));
  assert.equal(app.panelSummaries.map(p => p.name).join(','), '220V,LETO,MASTER,DOG');
  assert.equal(app.displayedMonitorPanels.map(p => p.name).join(','), '220V,LETO,MASTER,DOG');
  app.setMonitorPanelCollapsed('220V', true);
  assert.equal(app.displayedMonitorPanels[0].name, '220V');
  app.openMonitorPanel('MASTER');
  assert.equal(app.displayedMonitorPanels.map(p => p.name).join(','), 'MASTER');
  app.closeMonitorPanel();
}
for (const name of ['220', '220V', '220В', ' 220v ']) assert.equal(app.monitorPanelRank(name), 0);
for (const subNames of [['DOG', 'LETO'], ['ZETA', 'MASTER', 'ALPHA', '220V']]) {
  app.proxies = subNames.map((subName, i) => ({ ...base, subName, stableId: 'extra-' + i }));
  const expected = subNames.length === 2 ? 'LETO,DOG' : '220V,MASTER,ALPHA,ZETA';
  assert.equal(app.panelSummaries.map(p => p.name).join(','), expected);
}
console.log('PANEL_ORDER_PASS: 24 permutations; 220V,LETO,MASTER,DOG; aliases/fallback/collapse/full-view=PASS');
