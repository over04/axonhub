import { HTMLAttributes } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { PasswordInput } from '@/components/password-input';
import { passwordSchema } from '@/lib/validation';
import { usePublicAuthSettings, useSignUp } from '../../data/auth';

type SignUpFormProps = HTMLAttributes<HTMLFormElement>;

const createFormSchema = (t: (key: string) => string) =>
  z
    .object({
      lastName: z.string().min(1, { message: t('auth.signUp.validation.lastNameRequired') }),
      firstName: z.string().min(1, { message: t('auth.signUp.validation.firstNameRequired') }),
      email: z
        .string()
        .min(1, { message: t('auth.signUp.validation.emailRequired') })
        .email({ message: t('auth.signUp.validation.emailInvalid') }),
      password: passwordSchema(t),
      confirmPassword: z.string(),
      inviteCode: z.string().optional(),
    })
    .refine((data) => data.password === data.confirmPassword, {
      message: t('users.validation.passwordsNotMatch'),
      path: ['confirmPassword'],
    });

export function SignUpForm({ className, ...props }: SignUpFormProps) {
  const { t } = useTranslation();
  const { data: publicSettings, isLoading: isLoadingPublicSettings } = usePublicAuthSettings();
  const signUp = useSignUp();

  const formSchema = createFormSchema(t);
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      firstName: '',
      lastName: '',
      email: '',
      password: '',
      confirmPassword: '',
      inviteCode: '',
    },
  });

  function onSubmit(data: z.infer<typeof formSchema>) {
    signUp.mutate({
      firstName: data.firstName.trim(),
      lastName: data.lastName.trim(),
      email: data.email,
      password: data.password,
      inviteCode: data.inviteCode?.trim(),
    });
  }

  const publicMode = publicSettings?.publicMode ?? false;
  const inviteCodeRequired = publicSettings?.inviteCodeRequired ?? false;
  const isDisabled = isLoadingPublicSettings || signUp.isPending || !publicMode;

  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(onSubmit)} className={cn('grid gap-6', className)} {...props}>
        {!publicMode && !isLoadingPublicSettings && (
          <div className='rounded-md border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-600'>
            {t('auth.signUp.publicDisabled')}
          </div>
        )}
        <div className='grid gap-4 sm:grid-cols-2'>
          <FormField
            control={form.control}
            name='lastName'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-sm font-medium text-slate-700'>{t('users.form.lastName')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={t('profile.form.fields.lastName.placeholder')}
                    className='border-slate-300 !bg-white text-slate-800 transition-all duration-300 placeholder:text-slate-400 focus:border-slate-500 focus:!bg-white focus:ring-2 focus:ring-slate-200'
                    {...field}
                  />
                </FormControl>
                <FormMessage className='text-red-600' />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='firstName'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-sm font-medium text-slate-700'>{t('users.form.firstName')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={t('profile.form.fields.firstName.placeholder')}
                    className='border-slate-300 !bg-white text-slate-800 transition-all duration-300 placeholder:text-slate-400 focus:border-slate-500 focus:!bg-white focus:ring-2 focus:ring-slate-200'
                    {...field}
                  />
                </FormControl>
                <FormMessage className='text-red-600' />
              </FormItem>
            )}
          />
        </div>
        <FormField
          control={form.control}
          name='email'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-sm font-medium text-slate-700'>{t('auth.signIn.form.email.label')}</FormLabel>
              <FormControl>
                <Input
                  type='email'
                  placeholder={t('auth.signIn.form.email.placeholder')}
                  className='border-slate-300 !bg-white text-slate-800 transition-all duration-300 placeholder:text-slate-400 focus:border-slate-500 focus:!bg-white focus:ring-2 focus:ring-slate-200'
                  {...field}
                />
              </FormControl>
              <FormMessage className='text-red-600' />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='password'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-sm font-medium text-slate-700'>{t('auth.signIn.form.password.label')}</FormLabel>
              <FormControl>
                <PasswordInput
                  placeholder={t('auth.signIn.form.password.placeholder')}
                  className='border-slate-300 bg-white text-slate-800 backdrop-blur-sm transition-all duration-300 placeholder:text-slate-400 focus:border-slate-500 focus:bg-white focus:ring-2 focus:ring-slate-200'
                  {...field}
                />
              </FormControl>
              <FormMessage className='text-red-600' />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='confirmPassword'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-sm font-medium text-slate-700'>{t('users.form.confirmPassword')}</FormLabel>
              <FormControl>
                <PasswordInput
                  placeholder={t('auth.signIn.form.password.placeholder')}
                  className='border-slate-300 bg-white text-slate-800 backdrop-blur-sm transition-all duration-300 placeholder:text-slate-400 focus:border-slate-500 focus:bg-white focus:ring-2 focus:ring-slate-200'
                  {...field}
                />
              </FormControl>
              <FormMessage className='text-red-600' />
            </FormItem>
          )}
        />
        {inviteCodeRequired && (
          <FormField
            control={form.control}
            name='inviteCode'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-sm font-medium text-slate-700'>{t('auth.signUp.form.inviteCode.label')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={t('auth.signUp.form.inviteCode.placeholder')}
                    className='border-slate-300 !bg-white text-slate-800 transition-all duration-300 placeholder:text-slate-400 focus:border-slate-500 focus:!bg-white focus:ring-2 focus:ring-slate-200'
                    {...field}
                  />
                </FormControl>
                <FormMessage className='text-red-600' />
              </FormItem>
            )}
          />
        )}
        <Button
          type='submit'
          className='mt-2 w-full rounded-lg bg-slate-800 px-6 py-3 font-medium text-white shadow-lg transition-all duration-300 hover:bg-slate-700 hover:shadow-xl focus:ring-2 focus:ring-slate-500 focus:ring-offset-2 disabled:opacity-50'
          disabled={isDisabled}
        >
          {signUp.isPending ? t('auth.signUp.form.creating') : t('auth.signUp.form.createAccount')}
        </Button>
      </form>
    </Form>
  );
}
