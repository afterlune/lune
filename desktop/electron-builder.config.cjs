module.exports = {
  appId: 'com.afterlune.lune',
  productName: 'Lune',
  directories: { output: 'release' },
  files: ['out/**/*', 'package.json'],
  extraResources: [{ from: `resources/bin/${process.env.LUNE_DESKTOP_TARGET || process.platform}`, to: 'bin' }],
  asar: true,
  win: { target: ['nsis'], artifactName: 'Lune-${version}-Windows-${arch}.${ext}' },
  nsis: { oneClick: false, allowToChangeInstallationDirectory: true },
  linux: { target: ['AppImage'], category: 'Development', syncDesktopName: true, artifactName: 'Lune-${version}-Linux-${arch}.${ext}' },
}
