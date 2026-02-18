import type {ReactNode} from 'react';
import clsx from 'clsx';
import Heading from '@theme/Heading';
import {Layers, FolderSearch, Shield} from 'lucide-react';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  Icon: React.ComponentType<{size?: number; strokeWidth?: number; className?: string}>;
  description: ReactNode;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Tiered Escalation',
    Icon: Layers,
    description: (
      <>
        Haiku observes for pennies. Sonnet investigates and applies safe fixes.
        Opus handles full redeployments. Smarter models are only invoked when
        something is actually broken.
      </>
    ),
  },
  {
    title: 'Repo Discovery',
    Icon: FolderSearch,
    description: (
      <>
        Mount your infrastructure repos and Claude discovers services
        automatically. Ansible, Docker Compose, Helm — it reads your manifests
        and figures out the rest.
      </>
    ),
  },
  {
    title: 'Safety Guardrails',
    Icon: Shield,
    description: (
      <>
        Cooldown limits prevent runaway restarts. Permission tiers enforce
        what each model can do. Data volumes are never deleted. When in doubt,
        it alerts a human instead of retrying.
      </>
    ),
  },
];

function Feature({title, Icon, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center">
        <Icon size={64} strokeWidth={1.5} className={styles.featureIcon} />
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
