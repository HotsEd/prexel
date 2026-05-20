/**
 * EN-US fragment for agent C — i18n sweep in Settings.
 *
 * Mirrors the PT-BR sibling file. Keep keys/structure identical so the
 * downstream merger can zip them without ambiguity.
 */
export default {
    settings: {
        members: {
            title: 'Members',
            sub: 'People who can access this Prexel instance.',
            count: '{n} member | {n} members',
            countZero: 'No members',
            new: 'New member',
            edit: 'Edit member',
            initialPassword: 'Initial password',
            status: {
                label: 'Status',
                active: 'Active',
                inactive: 'Inactive',
                blocked: 'Blocked',
            },
            roleNoneGlobal: 'No global role',
            teamsGlobalTag: 'Global',
            createdToast: 'Member created.',
            updatedToast: 'Member updated.',
            removedToast: 'Member removed.',
            remove: {
                title: 'Remove member',
                body: 'Remove {email} from this instance?',
                accept: 'Remove',
            },
            columns: {
                person: 'Person',
                role: 'Role',
                teams: 'Teams',
                status: 'Status',
            },
        },
        security: {
            twoFactor: {
                sub: 'Add an extra layer of security to your account.',
                qrAlt: 'TOTP QR code',
                disableBody: 'To disable 2FA, enter your password and the current code from your authenticator app.',
                passwordLabel: 'Password',
                totpLabel: 'Current TOTP code',
                regenerate: 'Regenerate',
                acknowledge: 'Got it',
                passwordRequired: 'Password required.',
                disableButton: 'Disable 2FA',
                disableMissingFields: 'Password and current TOTP code are required.',
                disabledToast: '2FA disabled.',
            },
        },
    },
} as const
