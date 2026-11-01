---
layout: home

hero:
  name: lys-polkit
  text: The lys desktop's polkit agent
  tagline: When a program asks polkitd for an action that needs authentication, lys-polkit runs the PAM conversation and asks in lys-shell's dialog. It replaces hyprpolkitagent.
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
    details: Whatever PAM asks for appears in lys-shell's authentication dialog, from a password prompt to a fingerprint reader's message.
  - title: Runs as a user service
    details: A systemd user unit wanted by the graphical session. It finds the session polkitd expects it to serve, from inside a session or outside one.
  - title: Fails where you can see it
    details: If another agent already serves the session it exits with polkitd's message and leaves a failed unit, instead of running unregistered.
  - title: Tested on a real polkit
    details: A NixOS VM authenticates pkexec through lys-polkit against polkit 127, with the right password, a wrong one, a cancel and no agent.
---
