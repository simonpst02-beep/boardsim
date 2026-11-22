# BoardSim

Visionneuse de schémas PDF et de vues de carte (.brd, BRD2, .bdv, .asc, .bvr, .cad, GenCAD) avec carnet de réparation, en français.

## Utilisation

1. Téléchargez `BoardSim.exe` depuis l'onglet **Releases** du dépôt.
2. Placez-le dans un dossier à vous, par exemple `Documents\BoardSim`. **Pas dans Program Files**, sinon la mise à jour automatique ne pourra pas remplacer le fichier.
3. Double-cliquez dessus. Le logiciel s'ouvre dans sa propre fenêtre (Edge ou Chrome en mode application). Il fonctionne sans internet.

Au premier lancement, Windows peut afficher « Windows a protégé votre ordinateur » parce que le programme n'est pas signé : cliquez sur **Informations complémentaires** puis **Exécuter quand même**.

Le carnet de réparation est enregistré dans `%APPDATA%\BoardSim\carnet.json`, avec une copie de secours `carnet.json.bak`.

## Mises à jour

Au démarrage, le logiciel regarde la dernière version publiée dans les Releases du dépôt GitHub. Si elle est plus récente, un bandeau propose **Mettre à jour** : le logiciel télécharge le nouvel `BoardSim.exe`, vérifie son empreinte SHA-256, remplace l'ancien et redémarre.

## Publier une nouvelle version

Tout est automatique avec GitHub Actions (fichier `.github/workflows/release.yml`).

1. Faites vos modifications.
2. Changez le numéro dans le fichier `VERSION` (par exemple `v1.1.0`). On peut le faire directement sur le site GitHub avec le crayon ✏️.
3. Envoyez (commit).

Quelques minutes plus tard, `BoardSim.exe` et `BoardSim.exe.sha256` apparaissent dans les Releases, et tous les BoardSim installés proposent la mise à jour. Le numéro doit toujours augmenter : v1.1.0, v1.2.0, v2.0.0…

## Compiler soi-même

Il faut Go 1.22 ou plus récent :

```
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -X main.version=v1.0.0 -X main.repo=simonpst02-beep/boardsim" -o BoardSim.exe .
```

## Contenu

- `main.go` : le programme Windows (serveur local, fenêtre, enregistrement, mises à jour).
- `web/index.html` : l'interface.
- `web/vendor/` : pdf.js de Mozilla (licence Apache 2.0, voir `LICENSE-pdfjs.txt`).
