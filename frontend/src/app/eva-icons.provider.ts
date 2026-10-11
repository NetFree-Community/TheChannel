import { inject, provideAppInitializer } from '@angular/core';
import { NbIconLibraries, NbSvgIcon } from '@nebular/theme';
import { EVA_ICON_SUBSET } from './eva-icons.subset';

// Registers only the Eva icons the app uses (see scripts/gen-eva-subset.mjs)
// in place of NbEvaIconsModule, which shipped the whole 237 KB pack for a few
// dozen names. Same pack name and default, so `<nb-icon icon="...">` and
// menu items are unchanged.
class EvaSubsetIcon extends NbSvgIcon {
  constructor(name: string, inner: string) {
    super(name, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="100%" height="100%" fill="currentColor" class="eva eva-${name}">${inner}</svg>`);
  }
}

export function provideEvaIconSubset() {
  return provideAppInitializer(() => {
    const libraries = inject(NbIconLibraries);
    const icons: Record<string, NbSvgIcon> = {};
    for (const [name, inner] of Object.entries(EVA_ICON_SUBSET)) {
      icons[name] = new EvaSubsetIcon(name, inner);
    }
    libraries.registerSvgPack('eva', icons);
    libraries.setDefaultPack('eva');
  });
}
