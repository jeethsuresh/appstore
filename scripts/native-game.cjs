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
      'Smooth movement between simulation updates with continuous actor interpolation.',
      'Keep the camera steady inside a movement dead zone and ease into follow motion beyond it.',
      'Use a fixed camera angle and grounded walking animation with stable combat framing.',
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
