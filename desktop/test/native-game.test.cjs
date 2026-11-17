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
  assert.throws(() => game('../unsafe'));
  assert.throws(() => game('__proto__'));
});
