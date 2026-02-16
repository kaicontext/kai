# IP Ownership Policy

*Internal document for legal reference and investor diligence.*

## Code Ownership

### Company-authored code

All code written by founders and employees is owned by the company through standard employment agreements with invention assignment clauses. Founders have executed IP assignment agreements transferring all project-related intellectual property to the company.

### Contractor contributions

Contractors working on Kai must sign an invention assignment agreement that assigns all work product to the company. This covers:
- Code contributions
- Documentation
- Architecture and design work
- Any related inventions

### Open-source contributions

External contributors to the public repository contribute under the Apache License, Version 2.0 terms. The DCO (Developer Certificate of Origin) confirms that:
1. The contributor has the right to submit the code
2. The contribution is licensed under Apache 2.0
3. The contributor grants the associated patent license

No additional IP assignment (CLA) is required from external contributors. The company does not claim ownership of external contributions — they are licensed, not assigned.

## Pre-existing IP

Any pre-existing intellectual property incorporated into Kai is documented and licensed appropriately:
- Third-party dependencies are tracked in `go.mod` files with compatible licenses
- Tree-sitter grammars are MIT/Apache licensed
- No proprietary third-party code is included in the OSS repository

## License Structure

| Asset | Ownership | License |
|-------|-----------|---------|
| kai-core, kai-cli, kailab source | Company | Apache 2.0 (public) |
| kailab-control source | Company | Apache 2.0 (public) |
| Kai Cloud infrastructure | Company | Proprietary |
| Kai Cloud features (analytics, scoring, enterprise) | Company | Proprietary |
| External contributions | Contributors | Apache 2.0 (licensed to project) |
| Kai trademark and branding | Company | Not licensed |

## Trademark

The Kai name and logo are trademarks of the company. Apache 2.0 does not grant trademark rights (Section 6). Third parties may not use the Kai name or logo to imply endorsement without permission.

## Summary

- All core IP is owned by the company
- OSS code is licensed under Apache 2.0 (permissive, with patent grant)
- External contributions are licensed, not assigned
- Proprietary features remain closed source
- Clean IP chain from founders → company → public license
