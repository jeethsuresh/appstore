'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const game = require('../../scripts/native-game.cjs');
const catalog = require('../../config/apps.json');

test('native signing metadata matches independently versioned Bazaar listings', () => {
  for (const id of ['thornwake', 'ember-rest']) {
    const product = game(id);
    const app = catalog.find(entry => entry.id === id);
    assert.equal(app.name, product.name);
    assert.equal(app.desktop, true);
    assert.equal(app.desktopReleaseTagPrefix, id + '-');
    assert.ok(app.macOSNotarizedFrom > 0);
    assert.match(product.library, /^lib[a-z_]+\.dylib$/);
    assert.match(product.bundle, /^cool\.gravitas\.[a-z]+$/);
    assert.ok(product.changes.length > 0);
  }
  assert.equal(game().id, 'thornwake');
  assert.equal(game('ember-rest').name, 'Ember Rest');
  assert.equal(game('ember-rest').stem, 'EmberRest');
  assert.equal(game('ember-rest').replay, 'res://assets/validation.emberreplay');
  assert.throws(() => game('../unsafe'));
  assert.throws(() => game('__proto__'));
});


test('Railyard identifies its pure Godot export and validates a completed installed match', () => {
  const config = game('railyard-fps');
  assert.equal(config.name, 'Railyard FPS');
  assert.equal(config.bundle, 'cool.gravitas.railyard-fps');
  assert.equal(config.pck, 'Railyard FPS.pck');
  assert.equal(config.library, undefined);
  const { verifyFreight } = require('../../scripts/smoke-thornwake-release.cjs');
  const report = { map: 'freight', mode: 'deathmatch', server: true, actors: 2, peers: 0, elapsed: 2.0167, positions: { 1: [0, 1, 0], 100: [3, 1, 0] } };
  verifyFreight(report);
  for (const change of [{ map: 'lockout' }, { elapsed: 0 }, { actors: 1 }, { positions: { 1: [], 100: [] } }]) {
    assert.throws(() => verifyFreight({ ...report, ...change }));
  }
});
