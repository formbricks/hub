import type { ReactNode } from "react";
import clsx from "clsx";
import Heading from "@theme/Heading";
import styles from "./styles.module.css";

type FeatureItem = {
  title: string;
  icon: string;
  description: ReactNode;
};

const FeatureList: FeatureItem[] = [
  {
    title: "Unified Data Repository",
    icon: "🗄️",
    description: (
      <>
        Collect experience data from surveys, reviews, support tickets, and more
        into one centralized, queryable data hub. No vendor lock-in.
      </>
    ),
  },
  {
    title: "AI-Powered Insights",
    icon: "🤖",
    description: (
      <>
        Automatic sentiment analysis, emotion detection, and topic extraction
        powered by OpenAI. Semantic search with pgvector embeddings.
      </>
    ),
  },
  {
    title: "Analytics-Ready Schema",
    icon: "📊",
    description: (
      <>
        Optimized for direct SQL queries and seamless integration with BI tools like Apache Superset, Power BI, Tableau, and Looker.
      </>
    ),
  },
];

function Feature({ title, icon, description }: FeatureItem) {
  return (
    <div className={clsx("col col--4")}>
      <div className="text--center">
        <span style={{ fontSize: "4rem" }} role="img" aria-label={title}>
          {icon}
        </span>
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
