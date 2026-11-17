'use strict';

const games = {
  thornwake: {
    id: 'thornwake', name: 'Thornwake', stem: 'Thornwake', bundle: 'cool.gravitas.thornwake',
    library: 'libthornwake_core.dylib', frame: 'THORNWAKE_FRAME', enter: false,
    changes: ['Sign in with Gravitas and host or join native Thornwake multiplayer lobbies.'],
  },
  'ember-rest': {
    id: 'ember-rest', name: 'Ember Rest', stem: 'EmberRest', bundle: 'cool.gravitas.emberrest',
    library: 'libember_core.dylib', frame: 'EMBER_FRAME', enter: true, replay: 'res://assets/validation.emberreplay',
    changes: [
      'Remove whole-dungeon navigation stalls when enemies spawn or pursue moving players.',
      'Batch enemy rendering, prepare models before encounters, and reuse bounded combat effects for smoother crowds.',
      'Add curved weapon trails, impact poses, readable windups, critical numbers, pickup chimes and reactive health meters.',
      'Follow macOS Reduce Motion by default and offer a saved calm-motion setting while retaining the steady camera and movement dead zone.',
    ],
  },
};
function game(id = 'thornwake') {
  if (!Object.hasOwn(games, id)) throw new Error('Unknown native game');
  return games[id];
}
module.exports = game;
if (require.main === module) {
  const value = game(process.argv[2]);
  console.log([value.name, value.stem, value.bundle, value.library].join('\t'));
}
