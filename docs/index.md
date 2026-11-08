---
layout: home

hero:
  name: lys-polkit
  text: The lys desktop's polkit agent
  tagline: Answers polkitd's authentication requests through lys-shell's dialog.
  image:
    src: /lys-polkit.svg
    alt: lys-polkit
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: Install
      link: /guide/install
    - theme: alt
      text: GitHub
      link: https://github.com/stubbedev/lys-polkit

features:
  - title: Password or fingerprint
    details: Every PAM prompt and message appears in lys-shell's authentication dialog.
  - title: Systemd user service
    details: Started with graphical-session.target; registers for the user's session from inside or outside it.
  - title: Visible failures
    details: A refused registration exits with polkitd's message and leaves a failed unit.
  - title: Retained authorizations
    details: Lists auth_admin_keep authorizations on the session bus and revokes them on demand.
---
