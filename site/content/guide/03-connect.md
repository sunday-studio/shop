---
title: Connect the iPhone app
group: Getting started
summary: Pair your phone with your server using its address and the password.
---

## Get the app

Shop is not on the App Store yet. For now you build the app yourself and install it on your iPhone.

1. Open `ios/Shop/Shop.xcodeproj` in Xcode (the project needs a recent version, set up for iOS 26.5).
2. Select the Shop target, open Signing & Capabilities, and choose your own team. Change the bundle identifier if Xcode asks you to.
3. Pick your iPhone (iOS 26.5 or later) and run the app.

The full setup notes are in [IOS-SETUP.md](https://github.com/sunday-studio/shop/blob/main/docs/IOS-SETUP.md).

## Connect

1. Open the app and continue past the welcome screen.
2. Step 1 asks where your server is. Enter its address, for example `http://192.168.1.20:8080` or `http://shop.local:8080`.
3. Tap **Test connection**. When the app says it found a Shop server, tap **Continue**.
4. Step 2 asks for the pairing password that `make setup` printed. Enter it and tap **Connect**.
5. When the app says you're connected, scan your first receipt or go to Home.

Which address to use:

- **A real iPhone:** the server machine's address on your network, like the examples above. `localhost` points at the phone itself and will not work.
- **The iOS Simulator on the same Mac as the server:** `http://localhost:8080`.

The first time, the iPhone asks to allow access to your local network. Allow it, or the app cannot reach the server.

The app keeps its own key for this phone in the iPhone Keychain. The password itself is not stored.

## Away from home

[Tailscale](https://tailscale.com) lets your phone reach the server from anywhere without opening any ports on your router.

1. Install Tailscale on the server machine and on the iPhone, and sign both into the same tailnet.
2. Find the machine's name.
   ```bash
   tailscale status
   ```
3. Use `http://<name>.<tailnet>.ts.net:8080` as the server address in the app.

For HTTPS, run `tailscale serve --bg 8080` on the server and use `https://<name>.<tailnet>.ts.net` instead.

## If you lose the password

On the server, make a new one. The app shows this command on the password step too.

```bash
docker compose -f deploy/docker-compose.yml exec api shop password generate
```

Phones that are already connected stay connected. To disconnect all of them as well, add `-revoke-devices`. One phone can disconnect itself from Settings with **Disconnect this iPhone**.

> **Note:** Five wrong passwords from one address lock that address out for 15 minutes.
