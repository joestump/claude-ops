---
status: accepted
date: 2026-03-09
---

# SPEC-0029: Progressive Web App and Mobile Responsive Dashboard

## Overview

Convert the Claude Ops dashboard from a desktop-only web application into a Progressive Web App (PWA) with mobile-first responsive design. The dashboard currently uses a fixed 224px sidebar, hardcoded 4-column grids, 10-column tables, and zero CSS media queries — making it unusable on phones. This spec defines requirements for installability, responsive layout, touch-friendly interactions, and offline shell caching. See SPEC-0008 (Go/HTMX/DaisyUI Web Dashboard) for the existing dashboard architecture.

## Requirements

### Requirement: Collapsible Sidebar Navigation

The sidebar navigation MUST be hidden on mobile viewports and accessible via a hamburger menu toggle. On desktop viewports (lg breakpoint, 1024px+), the sidebar MUST remain visible as a fixed `w-56` column matching the current layout. The hamburger button MUST be visible only on viewports below the lg breakpoint. When the mobile menu is open, it MUST overlay the content as a slide-in drawer or full-width panel. Tapping outside the drawer or pressing a close button MUST dismiss it. The "Run Now" button MUST remain accessible in the mobile navigation.

#### Scenario: Desktop viewport renders fixed sidebar

- **WHEN** the viewport width is 1024px or greater
- **THEN** the sidebar is visible as a fixed `w-56` column and the hamburger button is hidden

#### Scenario: Mobile viewport hides sidebar and shows hamburger

- **WHEN** the viewport width is below 1024px
- **THEN** the sidebar is hidden and a hamburger menu button is visible in a top bar

#### Scenario: Hamburger toggle opens mobile navigation

- **WHEN** the user taps the hamburger button on a mobile viewport
- **THEN** the sidebar slides in as an overlay drawer with all navigation links and the Run Now button

#### Scenario: Tapping outside drawer closes it

- **WHEN** the mobile drawer is open and the user taps outside the drawer area
- **THEN** the drawer closes and the content is fully visible

### Requirement: Web App Manifest

The application MUST serve a `manifest.json` file that enables PWA installation. The manifest MUST include `name`, `short_name`, `start_url`, `display: standalone`, `background_color`, and `theme_color` (#D4764E). The manifest MUST reference at least two icon sizes: 192x192 and 512x512 PNG. The layout template MUST include a `<link rel="manifest">` tag.

#### Scenario: Browser shows install prompt

- **WHEN** a user visits the dashboard in a PWA-capable browser
- **THEN** the browser MAY offer an "Add to Home Screen" / install prompt based on the valid manifest

#### Scenario: Installed app launches in standalone mode

- **WHEN** the user launches Claude Ops from their home screen after installing
- **THEN** the app opens in standalone mode without browser chrome

### Requirement: Apple Mobile Web App Meta Tags

The layout template MUST include `<meta name="apple-mobile-web-app-capable" content="yes">`, `<meta name="apple-mobile-web-app-status-bar-style">`, `<meta name="theme-color">`, and `<link rel="apple-touch-icon">`. A favicon MUST be served at `/favicon.ico` or via `<link rel="icon">`.

#### Scenario: iOS home screen icon renders correctly

- **WHEN** an iOS user adds the app to their home screen
- **THEN** the apple-touch-icon is used as the app icon and the app launches without Safari chrome

### Requirement: Service Worker for Offline Shell

A service worker MUST be registered to cache the application shell (layout HTML, style.css, logo.svg, manifest.json, icons). The service worker MUST use a cache-first strategy for static assets and a network-first strategy for `/api/v1/` endpoints, falling back to cached responses when offline. The service worker MUST NOT cache SSE streams (`/sessions/{id}/stream`) or POST requests. The service worker SHOULD implement cache versioning so that deploying a new version invalidates stale caches.

#### Scenario: App shell loads offline

- **WHEN** the user opens the installed PWA without network connectivity
- **THEN** the cached app shell (layout, CSS, logo) renders and a message indicates offline status

#### Scenario: API data served from cache when offline

- **WHEN** the user navigates to sessions or events while offline
- **THEN** the last cached API response is displayed with an indicator that data may be stale

#### Scenario: New deployment invalidates cache

- **WHEN** a new version of the service worker is deployed
- **THEN** the old cache is deleted during the activate event and fresh assets are fetched

### Requirement: Responsive Stats HUD Grid

The TL;DR page stats grid MUST use responsive breakpoints: 1 column on mobile (below sm), 2 columns on sm (640px+), and 4 columns on lg (1024px+). The grid MUST NOT overflow horizontally on any supported viewport width.

#### Scenario: Stats render as single column on phone

- **WHEN** the viewport is 375px wide
- **THEN** each stat tile occupies the full width, stacked vertically

#### Scenario: Stats render as 2x4 grid on tablet

- **WHEN** the viewport is 768px wide
- **THEN** stats render in a 2-column layout

#### Scenario: Stats render as original 4-column on desktop

- **WHEN** the viewport is 1024px or wider
- **THEN** stats render in the original 4-column layout

### Requirement: Responsive Table Layouts

Data tables (sessions, memories, cooldowns) MUST remain usable on mobile viewports. Low-priority columns (Trigger, Exit, Turns for sessions; Actions column width for memories) SHOULD be hidden on viewports below the md breakpoint using `hidden md:table-cell`. Tables MUST retain `overflow-x-auto` as a fallback for remaining columns. Table row vertical padding MUST be sufficient for touch interaction (minimum 44px effective row height).

#### Scenario: Sessions table hides low-priority columns on mobile

- **WHEN** the viewport is below 768px
- **THEN** the Trigger, Turns, and Exit columns are hidden and the remaining columns fit without horizontal scroll

#### Scenario: Table rows are touch-friendly

- **WHEN** the user taps a session row on a touchscreen
- **THEN** the tap target height is at least 44px

### Requirement: Touch Target Minimum Size

All interactive elements (navigation links, buttons, badges, checkboxes, form controls) MUST have a minimum touch target of 44x44 CSS pixels per WCAG 2.1 Success Criterion 2.5.8. This applies to effective tap area including padding, not just the visible element. The `.nav-link` padding MUST increase to at least `0.75rem 1.25rem`. Button padding (`.btn-primary`, `.btn-secondary`, `.btn-danger`) MUST produce at least 44px total height. The `.checkbox-field` MUST be at least `1.25rem` square.

#### Scenario: Navigation link meets touch target

- **WHEN** a user taps a sidebar navigation link on a touchscreen
- **THEN** the effective touch target is at least 44px tall

#### Scenario: Primary button meets touch target

- **WHEN** a user taps the Run button in the modal
- **THEN** the effective touch target is at least 44px tall and 44px wide

### Requirement: Responsive Main Content Padding

The main content area MUST use progressive padding: `px-4` on mobile (below sm), `px-6` on sm (640px+), and `px-8` on lg (1024px+). The current fixed `p-8` MUST be replaced with responsive padding classes.

#### Scenario: Mobile content has compact padding

- **WHEN** the viewport is 375px wide
- **THEN** the main content has 16px horizontal padding (px-4)

#### Scenario: Desktop content retains original padding

- **WHEN** the viewport is 1024px or wider
- **THEN** the main content has 32px padding (p-8) matching the current design

### Requirement: Body Layout Fix

The body element MUST NOT use `overflow-hidden` as it prevents scrolling when the mobile keyboard appears. The layout MUST use `min-h-screen` instead of `h-screen` on the body to allow natural document flow on mobile. The sidebar and main content flex container MUST support vertical stacking on mobile viewports.

#### Scenario: Mobile keyboard does not block content

- **WHEN** the Run Now modal is open on a mobile device and the keyboard appears
- **THEN** the user can scroll to see the form fields and submit button

#### Scenario: Content scrolls naturally on mobile

- **WHEN** page content exceeds the viewport height on a mobile device
- **THEN** the page scrolls vertically without being clipped

### Requirement: Session Controls Mobile Positioning

The session follow/stop button cluster (`.session-controls`) MUST remain accessible on mobile viewports. The cluster SHOULD use `bottom: 1rem; right: 1rem` on mobile to avoid overlapping content. Button sizes within the cluster MUST meet the 44px minimum touch target.

#### Scenario: Follow and stop buttons visible on mobile

- **WHEN** viewing a running session on a 375px viewport
- **THEN** the follow and stop buttons are visible, tappable, and do not overlap the terminal content

### Requirement: Mobile Top Bar

On viewports below the lg breakpoint, a top bar MUST be displayed containing the Claude Ops logo/brand and the hamburger menu button. The top bar MUST be sticky at the top of the viewport. The top bar MUST NOT appear on desktop viewports where the sidebar is visible.

#### Scenario: Top bar appears on mobile

- **WHEN** the viewport is below 1024px
- **THEN** a sticky top bar with the logo and hamburger button is displayed

#### Scenario: Top bar hidden on desktop

- **WHEN** the viewport is 1024px or wider
- **THEN** no top bar is rendered (the sidebar provides navigation)
