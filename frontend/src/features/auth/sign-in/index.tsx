import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import AuthLayout from '../auth-layout';
import TwoColumnAuth from '../components/two-column-auth';
import AnimatedLineBackground from './components/animated-line-background';
import { UserAuthForm } from './components/user-auth-form';
import { SignUpForm } from './components/sign-up-form';
import './login-styles.css';

export default function SignIn() {
  const { t } = useTranslation();
  const [mode, setMode] = useState<'sign-in' | 'sign-up'>('sign-in');
  const isSignUp = mode === 'sign-up';

  return (
    <AuthLayout>
      <div data-testid='sign-in-animation-layer'>
        <AnimatedLineBackground key='optimized-layout' />
      </div>
      <TwoColumnAuth
        title={isSignUp ? t('auth.signUp.title') : t('auth.signIn.title')}
        description={isSignUp ? t('auth.signUp.subtitle') : t('auth.signIn.subtitle')}
        rightFooter={
          <p className='text-xs leading-relaxed text-slate-500 sm:text-sm'>
            {isSignUp ? t('auth.signUp.footer.agreement') : t('auth.signIn.footer.agreement')}
          </p>
        }
      >
        <div className='mb-6 grid grid-cols-2 rounded-lg bg-slate-100 p-1'>
          <Button
            type='button'
            variant={isSignUp ? 'ghost' : 'secondary'}
            className='h-9 rounded-md'
            onClick={() => setMode('sign-in')}
          >
            {t('auth.signIn.form.signInButton')}
          </Button>
          <Button
            type='button'
            variant={isSignUp ? 'secondary' : 'ghost'}
            className='h-9 rounded-md'
            onClick={() => setMode('sign-up')}
          >
            {t('auth.signUp.form.createAccount')}
          </Button>
        </div>
        {isSignUp ? <SignUpForm /> : <UserAuthForm />}
      </TwoColumnAuth>
    </AuthLayout>
  );
}
