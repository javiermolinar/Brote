// One mascot source for the embedded WebUI and extension development assets.
import {copyFile, mkdir} from 'node:fs/promises';
const root = new URL('../', import.meta.url);
const mascot = new URL('assets/brote-plant.png', root);
for (const directory of ['packages/web/public/', 'packages/vscode/assets/']) {
  const target = new URL(directory, root);
  await mkdir(target, {recursive: true});
  await copyFile(mascot, new URL('brote-plant.png', target));
}
